package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type runtimeState struct {
	cfg                        Config
	reader                     *bufio.Reader
	writer                     io.Writer
	ui                         UI
	bannerShown                bool
	promptTurn                 int
	prefillInput               string
	inputHistory               []string
	store                      *SessionStore
	session                    *Session
	agent                      *Agent
	perms                      *PermissionManager
	memory                     MemoryBundle
	longMem                    *PersistentMemoryStore
	evidence                   *EvidenceStore
	investigations             *InvestigationStore
	simulations                *SimulationStore
	functionFuzz               *FunctionFuzzStore
	fuzzCampaigns              *FuzzCampaignStore
	sourceScan                 *SourceScanStore
	hookOverrides              *HookOverrideStore
	checkpoints                *CheckpointManager
	autoCP                     *AutoCheckpointController
	verifyHistory              *VerificationHistoryStore
	backgroundJobs             *BackgroundJobManager
	modelRoutes                *ModelRouteScheduler
	hooks                      *HookRuntime
	hookWarns                  []string
	skills                     SkillCatalog
	skillWarns                 []string
	mcp                        *MCPManager
	mcpWarns                   []string
	ollamaModels               []OllamaModelInfo
	clientErr                  error
	workspace                  Workspace
	detectVerificationToolPath func(string) string
	goalReply                  func(context.Context, string) (string, error)
	interactive                bool
	strictConfig               bool
	configProfile              string
	outputMu                   sync.Mutex
	footerVisible              bool
	footerLineCount            int
	footerText                 string
	thinkingMu                 sync.Mutex
	thinkingStop               func()
	thinkingStartedAt          time.Time
	streamMu                   sync.Mutex
	streamingAssistant         bool
	streamedAssistantText      strings.Builder
	pendingAssistantSpacing    string
	assistantStreamInFence     bool
	assistantStreamLine        string
	suppressThinkingMu         sync.Mutex
	suppressThinking           bool
	thinkingStatusMu           sync.Mutex
	thinkingStatusOverride     string
	thinkingDetailMu           sync.Mutex
	thinkingDetailLines        []string
	requestCancelMu            sync.Mutex
	requestCancelPauses        int
	requestCancelPending       bool
	requestCancelIgnoreUntil   time.Time
	lastAssistantMu            sync.Mutex
	lastAssistantPrinted       string
	alwaysApprovePreview       bool
	alwaysApproveWrites        bool
	alwaysApproveVerification  bool
	autoAcceptPreviewOnce      bool
}

var assistantFollowOnPreamblePattern = regexp.MustCompile(`([\.\:\!\?\)])((?i:Now let me |Let me |Now I |I'll |I will |I need to |First, ))`)
var numberedPlanItemPattern = regexp.MustCompile(`^\d+[\.\)]\s+(.+)$`)
var bulletPlanItemPattern = regexp.MustCompile(`^[-*]\s+(.+)$`)

var fetchOpenRouterKeyStatus = FetchOpenRouterKeyStatus
var fetchOpenRouterCredits = FetchOpenRouterCredits
var fetchDeepSeekBalance = FetchDeepSeekBalance

const openRouterCurrentKeyDocsURL = "https://openrouter.ai/docs/api/api-reference/api-keys/get-current-key"
const openRouterCreditsDocsURL = "https://openrouter.ai/docs/api/api-reference/credits/get-credits"
const deepSeekBalanceDocsURL = "https://api-docs.deepseek.com/api/get-user-balance"
const deepSeekRateLimitDocsURL = "https://api-docs.deepseek.com/quick_start/rate_limit"
const anthropicBillingHelpURL = "https://support.anthropic.com/en/articles/8977456-how-do-i-pay-for-my-api-usage"
const anthropicUsageCostDocsURL = "https://platform.claude.com/docs/en/api/usage-cost-api"
const openAIUsageAPIDocsURL = "https://platform.openai.com/docs/api-reference/usage/costs?api-mode=responses&lang=curl"
const openAIPrepaidBillingHelpURL = "https://help.openai.com/en/articles/8264644-how-can-i-set-up-prepaid-billing"
const openAIUsageDashboardHelpURL = "https://help.openai.com/en/articles/10478918-api-usage-dashboard"

func run(args []string) error {
	if kernforgeCLIVersionRequest(args) {
		fmt.Fprintln(os.Stdout, "Kernforge "+currentVersion())
		return nil
	}
	if ok, topic := kernforgeCLIHelpRequest(args); ok {
		fmt.Fprint(os.Stdout, renderKernforgeCLIHelp(topic))
		return nil
	}

	fs := flag.NewFlagSet("kernforge", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		cwdFlag           string
		providerFlag      string
		profileFlag       string
		profileShortFlag  string
		modelFlag         string
		baseURLFlag       string
		imageFlag         string
		previewFileFlag   string
		previewResultFlag string
		viewerFileFlag    string
		viewerResultFlag  string
		promptFlag        string
		commandFlag       string
		goalFlag          string
		goalFileFlag      string
		goalMaxIterations int
		goalTimeBudget    string
		goalTokenBudget   int
		goalUntilComplete bool
		goalAutoRollback  bool
		resumeFlag        string
		permissionFlag    string
		yesFlag           bool
		strictConfigFlag  bool
		bypassHookTrust   bool
		mcpServerFlag     bool
		mcpDaemonProxy    bool
	)

	fs.StringVar(&cwdFlag, "cwd", "", "working directory")
	fs.StringVar(&providerFlag, "provider", "", "model provider")
	fs.StringVar(&profileFlag, "profile", "", "named user config profile")
	fs.StringVar(&profileShortFlag, "p", "", "shorthand for -profile")
	fs.StringVar(&modelFlag, "model", "", "model name")
	fs.StringVar(&baseURLFlag, "base-url", "", "provider base URL")
	fs.StringVar(&imageFlag, "image", "", "comma-separated image paths for -prompt mode")
	fs.StringVar(&imageFlag, "i", "", "shorthand for -image")
	fs.StringVar(&previewFileFlag, "preview-file", "", "internal diff preview file path")
	fs.StringVar(&previewResultFlag, "preview-result-file", "", "internal diff preview result path")
	fs.StringVar(&viewerFileFlag, "viewer-file", "", "internal viewer file path")
	fs.StringVar(&viewerResultFlag, "viewer-result-file", "", "internal viewer result path")
	fs.StringVar(&promptFlag, "prompt", "", "single prompt mode")
	fs.StringVar(&commandFlag, "command", "", "single slash command mode")
	fs.StringVar(&goalFlag, "goal", "", "autonomous goal objective")
	fs.StringVar(&goalFileFlag, "goal-file", "", "markdown file containing an autonomous goal objective")
	fs.IntVar(&goalMaxIterations, "goal-max-iterations", -1, "maximum autonomous goal iterations")
	fs.StringVar(&goalTimeBudget, "goal-time-budget", "", "autonomous goal time budget, for example 10m")
	fs.IntVar(&goalTokenBudget, "goal-token-budget", 0, "autonomous goal token budget estimate")
	fs.BoolVar(&goalUntilComplete, "goal-until-complete", false, "run autonomous goal until completion gates approve")
	fs.BoolVar(&goalAutoRollback, "goal-rollback-on-regression", false, "rollback autonomous goal checkpoints after repeated regression blockers")
	fs.StringVar(&resumeFlag, "resume", "", "resume session by id")
	fs.StringVar(&permissionFlag, "permission-mode", "", "permissions mode")
	fs.BoolVar(&yesFlag, "y", false, "auto-approve all permissions")
	fs.BoolVar(&strictConfigFlag, "strict-config", false, "fail on unknown configuration fields")
	fs.BoolVar(&bypassHookTrust, "dangerously-bypass-hook-trust", false, "run project-local hooks without saved project hook trust for this invocation")
	fs.BoolVar(&mcpServerFlag, "mcp-server", false, "run KernForge as a stdio MCP server")
	fs.BoolVar(&mcpDaemonProxy, "mcp-daemon-proxy", false, "proxy stdio MCP requests through the local KernForge daemon")

	if err := fs.Parse(args); err != nil {
		return err
	}

	cwd := cwdFlag
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		return err
	}

	if strings.TrimSpace(previewFileFlag) != "" {
		return RunDiffPreviewWindow(strings.TrimSpace(previewFileFlag), strings.TrimSpace(previewResultFlag))
	}
	if strings.TrimSpace(viewerFileFlag) != "" {
		return RunTextViewerWindow(strings.TrimSpace(viewerFileFlag), strings.TrimSpace(viewerResultFlag))
	}

	strictConfig := strictConfigFlag || strictConfigEnvEnabled()
	profileFlag = strings.TrimSpace(profileFlag)
	profileShortFlag = strings.TrimSpace(profileShortFlag)
	if profileFlag != "" && profileShortFlag != "" && profileFlag != profileShortFlag {
		return fmt.Errorf("-profile and -p specify different config profiles (%q and %q)", profileFlag, profileShortFlag)
	}
	profileFlag = firstNonBlankString(profileFlag, profileShortFlag)
	if fs.NArg() > 0 && strings.EqualFold(strings.TrimSpace(fs.Arg(0)), "doctor") {
		return runKernforgeDoctorCommand(cwd, fs.Args()[1:], kernforgeDoctorOptions{
			StrictConfig:       strictConfig,
			Profile:            profileFlag,
			ProviderOverride:   providerFlag,
			ModelOverride:      modelFlag,
			BaseURLOverride:    baseURLFlag,
			PermissionOverride: permissionFlag,
			ForceBypass:        yesFlag,
			BypassHookTrust:    bypassHookTrust,
		})
	}
	cfg, err := LoadConfigWithOptions(cwd, ConfigLoadOptions{StrictConfig: strictConfig, Profile: profileFlag})
	if err != nil {
		return err
	}
	// Detect and rewrite values that match the previous KernForge hard-coded
	// defaults (max_tool_iterations: 16, max_tokens: 4096). The notices are
	// printed once rt.writer is available below.
	legacyMigrations := MigrateLegacyConfigDefaultsWithProfile(cwd, profileFlag, &cfg)
	loadedProvider := normalizeProviderName(cfg.Provider)
	if providerFlag != "" {
		cfg.Provider = providerFlag
	}
	if modelFlag != "" {
		cfg.Model = modelFlag
	}
	if baseURLFlag != "" {
		cfg.BaseURL = baseURLFlag
	}
	if providerFlag != "" || modelFlag != "" {
		applyDefaultReasoningEffortForProvider(&cfg, cfg.Provider)
	}
	if providerFlag != "" && baseURLFlag == "" && loadedProvider != normalizeProviderName(providerFlag) {
		switch normalizeProviderName(providerFlag) {
		case "deepseek":
			cfg.BaseURL = normalizeDeepSeekBaseURL("")
		case "openai-codex":
			cfg.BaseURL = normalizeOpenAICodexBaseURL("")
		case "lmstudio", "vllm", "llama.cpp":
			cfg.BaseURL = normalizeLocalOpenAICompatibleBaseURL(providerFlag, "")
		case "codex-cli", "anthropic-claude-cli":
			cfg.BaseURL = ""
		}
	}
	if permissionFlag != "" {
		mode, ok := ParseModeStrict(permissionFlag)
		if !ok {
			return invalidPermissionModeError("permission-mode", permissionFlag)
		}
		cfg.PermissionMode = string(mode)
	}
	if yesFlag {
		cfg.PermissionMode = string(ModeBypass)
	}
	if bypassHookTrust {
		cfg.BypassHookTrust = true
	}
	if _, err := autoPopulateVerificationToolPaths(cwd, &cfg, detectWindowsVerificationToolPath); err != nil {
		return err
	}
	if strings.EqualFold(cfg.Provider, "ollama") && strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = normalizeOllamaBaseURL("")
	}
	if fs.NArg() > 0 && strings.EqualFold(strings.TrimSpace(fs.Arg(0)), "daemon") {
		return runKernforgeDaemonCommand(cwd, cfg, resumeFlag, fs.Args()[1:], mcpServerRunOptions{
			ConfigOverrides: mcpServerConfigOverrides{
				Provider:        providerFlag,
				Profile:         profileFlag,
				Model:           modelFlag,
				BaseURL:         baseURLFlag,
				PermissionMode:  permissionFlag,
				ForceBypass:     yesFlag,
				BypassHookTrust: bypassHookTrust,
			},
			LoadWorkspaceConfig: true,
			StrictConfig:        strictConfig,
		})
	}
	if mcpServerFlag {
		if mcpDaemonProxy {
			return runKernforgeMCPDaemonProxy(cwd, os.Stdin, os.Stdout)
		}
		return runKernforgeMCPServer(cwd, cfg, resumeFlag, os.Stdin, os.Stdout, mcpServerRunOptions{
			ConfigOverrides: mcpServerConfigOverrides{
				Provider:        providerFlag,
				Profile:         profileFlag,
				Model:           modelFlag,
				BaseURL:         baseURLFlag,
				PermissionMode:  permissionFlag,
				ForceBypass:     yesFlag,
				BypassHookTrust: bypassHookTrust,
			},
			LoadWorkspaceConfig: true,
			StrictConfig:        strictConfig,
		})
	}

	store := NewSessionStore(cfg.SessionDir)
	sess, err := loadOrCreateSession(store, resumeFlag, cwd, cfg)
	if err != nil {
		return err
	}
	mem, err := LoadMemory(sessionBaseWorkingDir(sess), cfg.MemoryFiles)
	if err != nil {
		return err
	}
	if strings.TrimSpace(imageFlag) != "" && strings.TrimSpace(promptFlag) == "" {
		return fmt.Errorf("-image requires -prompt; use @path/to/image.png in the interactive prompt")
	}
	if strings.TrimSpace(promptFlag) != "" && strings.TrimSpace(commandFlag) != "" {
		return fmt.Errorf("-prompt and -command cannot be used together")
	}
	if strings.TrimSpace(goalFlag) != "" || strings.TrimSpace(goalFileFlag) != "" {
		if strings.TrimSpace(promptFlag) != "" || strings.TrimSpace(commandFlag) != "" {
			return fmt.Errorf("-goal/-goal-file cannot be combined with -prompt or -command")
		}
	}
	cliImages, err := parseImageInputList(sess.WorkingDir, imageFlag)
	if err != nil {
		return err
	}

	rt := &runtimeState{
		cfg:            cfg,
		reader:         bufio.NewReader(os.Stdin),
		writer:         os.Stdout,
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
		strictConfig:   strictConfig,
		configProfile:  profileFlag,
		interactive:    runtimeShouldUseInteractiveLoop(promptFlag, commandFlag, goalFlag, goalFileFlag),
	}
	defer rt.closeExtensions()

	if rt.interactive {
		rt.showBanner()
	}
	rt.printBypassHookTrustNotice()
	rt.printLegacyConfigMigrationNotices(legacyMigrations)

	userInputRequests := NewUserInputRequestTracker()
	rt.perms = NewPermissionManager(ParseMode(sess.PermissionMode), rt.confirm)
	rt.perms.SetUserInputRequestTracker(userInputRequests)
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
		UserInputRequests:     userInputRequests,
		PrepareEdit:           rt.prepareEdit,
		PrepareEditAtRoot:     rt.prepareEditAtRoot,
		ReviewEdit:            rt.reviewProposedEdit,
		ReportProgress: func(message string) {
			rt.setThinkingStatus(message)
			rt.printProgressMessage(message)
		},
		CurrentSelection: func() *ViewerSelection {
			return rt.session.CurrentSelection()
		},
		PreviewEdit: rt.previewEdit,
		ConfirmVerification: func(plan VerificationPlan) (bool, error) {
			return rt.promptConfirmAutoVerify(plan)
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
		GoalSession:    rt.session,
		GoalStore:      rt.store,
	}
	rt.syncWorkspaceFromSession()
	if err := rt.ensureConfigured(); err != nil {
		return err
	}
	cfg = rt.cfg
	client, clientErr := NewProviderClient(rt.cfg)
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
		PromptConfirmAutoVerify: func(plan VerificationPlan) (bool, error) {
			return rt.promptConfirmAutoVerify(plan)
		},
		PromptResolveAutoVerifyFailure: func(report VerificationReport) (AutoVerifyFailureResolution, error) {
			return rt.promptResolveAutoVerifyFailure(report)
		},
		EmitAssistant: func(text string) {
			rt.printAssistantWhileThinking(text)
		},
		EmitAssistantPersistent: func(text string) {
			rt.printAssistant(text)
		},
		EmitAssistantDelta: func(text string) {
			rt.appendAssistantStream(text)
		},
		EmitProgress: func(text string) {
			// EmitProgress fires at meaningful phase transitions (plan-review
			// start, tool-budget extension, autoVerify start, etc.). Reset
			// the thinking timer so the spinner reports the *current* phase's
			// duration rather than carrying time over from a previous phase
			// or a goroutine that survived an earlier turn boundary.
			rt.resetThinkingTimer()
			rt.setThinkingStatus(compactThinkingStatus(rt.cfg, text))
			rt.printProgressMessage(text)
		},
		EmitProgressEvent: func(event ProgressEvent) {
			rt.resetThinkingTimer()
			rt.printProgressEvent(event)
		},
	}
	if rt.interactive {
		rt.agent.PromptContinueReviewRepair = func(message string) (bool, error) {
			return rt.promptContinueReviewRepair(message)
		}
	}
	rt.syncAgentReviewerClientFromConfig()
	rt.reloadExtensions()

	if promptFlag != "" {
		return rt.runSinglePrompt(promptFlag, cliImages)
	}
	if commandFlag != "" {
		return rt.runSingleCommand(commandFlag)
	}
	if strings.TrimSpace(goalFlag) != "" || strings.TrimSpace(goalFileFlag) != "" {
		if goalMaxIterations < -1 {
			return fmt.Errorf("-goal-max-iterations must be zero or greater")
		}
		if goalTokenBudget < 0 {
			return fmt.Errorf("-goal-token-budget must be zero or greater")
		}
		return rt.runSingleGoal(goalFlag, goalFileFlag, singleGoalOptions{
			MaxIterations: goalMaxIterations,
			TimeBudget:    goalTimeBudget,
			TokenBudget:   goalTokenBudget,
			UntilComplete: goalUntilComplete,
			AutoRollback:  goalAutoRollback,
		})
	}
	return rt.runREPL()
}

func loadOrCreateSession(store *SessionStore, resumeID, cwd string, cfg Config) (*Session, error) {
	var sess *Session
	var err error
	if strings.TrimSpace(resumeID) != "" {
		sess, err = store.Load(strings.TrimSpace(resumeID))
	} else {
		sess = NewSession(cwd, cfg.Provider, cfg.Model, cfg.BaseURL, cfg.PermissionMode)
		err = store.Save(sess)
	}
	if err != nil {
		return nil, err
	}
	sess.normalizeWorkingDirState()
	if wsSelections, loadErr := LoadWorkspaceSelections(sessionBaseWorkingDir(sess)); loadErr == nil && len(wsSelections) > 0 {
		for _, wSel := range wsSelections {
			sess.AddSelection(wSel)
		}
		_ = store.Save(sess)
	}
	return sess, nil
}

func buildRegistry(ws Workspace, mcp *MCPManager) *ToolRegistry {
	items := []Tool{
		NewListFilesTool(ws),
		NewReadFileTool(ws),
		NewViewImageTool(ws),
		NewGrepTool(ws),
		NewApplyEditProposalTool(ws),
		NewApplyPatchTool(ws),
		NewWriteFileTool(ws),
		NewReplaceInFileTool(ws),
		NewRunShellTool(ws),
		NewRunBackgroundShellTool(ws),
		NewRunShellBundleBackgroundTool(ws),
		NewCheckShellJobTool(ws),
		NewCheckShellBundleTool(ws),
		NewCancelShellJobTool(ws),
		NewCancelShellBundleTool(ws),
		NewGitAddTool(ws),
		NewGitCommitTool(ws),
		NewGitPushTool(ws),
		NewGitCreatePRTool(ws),
		NewGitStatusTool(ws),
		NewGitDiffTool(ws),
		NewUpdatePlanTool(ws),
	}
	if goalToolsAvailable(ws) {
		items = append(items,
			NewGetGoalTool(ws),
			NewCreateGoalTool(ws),
			NewUpdateGoalTool(ws),
		)
	}
	if mcp != nil {
		items = append(items, mcp.Tools()...)
	}
	return NewToolRegistry(items...)
}

func formatToolRegistrationIssues(issues []ToolRegistrationIssue) []string {
	if len(issues) == 0 {
		return nil
	}
	out := make([]string, 0, len(issues))
	for _, issue := range issues {
		summary := strings.TrimSpace(issue.Summary())
		if summary == "" {
			continue
		}
		out = append(out, summary)
	}
	return out
}

func (rt *runtimeState) runSinglePrompt(prompt string, images []MessageImage) error {
	if rt.clientErr != nil {
		return rt.clientErr
	}
	turnStartedAt := time.Now()
	ctx := context.Background()
	reply, err := rt.runAgentReplyWithImages(ctx, prompt, images)
	if err != nil {
		if errors.Is(err, ErrEditCanceled) {
			fmt.Fprintln(rt.writer, rt.ui.infoLine("Edit canceled."))
			rt.printTurnElapsed(turnStartedAt)
			return nil
		}
		if errors.Is(err, ErrWriteDenied) {
			fmt.Fprintln(rt.writer, rt.ui.infoLine("Write canceled."))
			rt.printTurnElapsed(turnStartedAt)
			return nil
		}
		rt.printTurnElapsed(turnStartedAt)
		return err
	}
	rt.printAssistant(reply)
	rt.printTurnElapsed(turnStartedAt)
	return nil
}

func (rt *runtimeState) runSingleCommand(command string) error {
	line := strings.TrimSpace(command)
	if line == "" {
		return fmt.Errorf("-command requires a slash command or !shell command")
	}
	if strings.HasPrefix(line, "!") {
		return rt.runShell(strings.TrimPrefix(line, "!"))
	}
	cmd, ok := ParseCommand(line)
	if !ok {
		return fmt.Errorf("-command only accepts slash commands or !shell commands: %s", command)
	}
	_, err := rt.handleCommand(cmd)
	if err != nil {
		if errors.Is(err, ErrPromptCanceled) {
			return nil
		}
		rt.noteCommandError(line, err)
	}
	return err
}

type singleGoalOptions struct {
	MaxIterations int
	TimeBudget    string
	TokenBudget   int
	UntilComplete bool
	AutoRollback  bool
}

func (rt *runtimeState) runSingleGoal(objective string, filePath string, options ...singleGoalOptions) error {
	if err := rt.requirePersistedGoalState(); err != nil {
		return err
	}
	fields := []string{"--run"}
	if len(options) > 0 {
		option := options[0]
		if option.UntilComplete {
			fields = append(fields, "--until-complete")
		} else if option.MaxIterations >= 0 {
			fields = append(fields, "--max-iterations", strconv.Itoa(option.MaxIterations))
		}
		if strings.TrimSpace(option.TimeBudget) != "" {
			fields = append(fields, "--time-budget", strings.TrimSpace(option.TimeBudget))
		}
		if option.TokenBudget > 0 {
			fields = append(fields, "--token-budget", strconv.Itoa(option.TokenBudget))
		}
		if option.AutoRollback {
			fields = append(fields, "--rollback-on-regression")
		}
	}
	if strings.TrimSpace(filePath) != "" {
		fields = append(fields, "--file", strings.TrimSpace(filePath))
	}
	if strings.TrimSpace(objective) != "" {
		fields = append(fields, strings.TrimSpace(objective))
	}
	return rt.handleGoalStart(fields)
}

func runtimeShouldUseInteractiveLoop(promptFlag string, commandFlag string, goalFlag string, goalFileFlag string) bool {
	return strings.TrimSpace(promptFlag) == "" &&
		strings.TrimSpace(commandFlag) == "" &&
		strings.TrimSpace(goalFlag) == "" &&
		strings.TrimSpace(goalFileFlag) == ""
}

func (rt *runtimeState) previewEdit(preview EditPreview) (bool, error) {
	if rt.alwaysApprovePreview {
		return true, nil
	}
	if rt.autoAcceptPreviewOnce {
		rt.autoAcceptPreviewOnce = false
		return true, nil
	}
	var (
		openPreview bool
		err         error
	)
	rt.withRequestCancelSuspended(func() {
		openPreview, err = rt.confirm(diffPreviewQuestion(rt.cfg))
	})
	if err != nil {
		return false, err
	}
	if rt.autoAcceptPreviewOnce {
		rt.autoAcceptPreviewOnce = false
		return true, nil
	}
	if !openPreview {
		return false, ErrEditCanceled
	}
	return rt.openEditPreview(preview)
}

func (rt *runtimeState) promptContinueReviewRepair(message string) (bool, error) {
	message = strings.TrimSpace(message)
	if message != "" {
		rt.printAssistant(message)
	}
	if !rt.interactive {
		return false, nil
	}
	var (
		confirmed bool
		err       error
	)
	rt.withRequestCancelSuspended(func() {
		confirmed, err = rt.confirm(reviewRepairContinuationQuestion(rt.cfg))
	})
	if err != nil {
		return false, err
	}
	return confirmed, nil
}

func (rt *runtimeState) openEditPreview(preview EditPreview) (bool, error) {
	ok, err := OpenDiffPreview(preview)
	if err == nil {
		return ok, nil
	}
	fmt.Fprintln(rt.writer, rt.ui.warnLine(localizedText(rt.cfg, "Falling back to terminal diff preview: ", "터미널 변경 미리보기로 전환합니다: ")+err.Error()))
	fmt.Fprintln(rt.writer, rt.ui.section(localizedText(rt.cfg, "Diff Preview", "변경 미리보기")))
	if strings.TrimSpace(preview.Title) != "" {
		fmt.Fprintln(rt.writer, rt.ui.infoLine(preview.Title))
	}
	fmt.Fprintln(rt.writer, preview.Preview)
	return true, nil
}

func (rt *runtimeState) reviewProposedEdit(ctx context.Context, preview EditPreview) error {
	if rt == nil || rt.agent == nil {
		return nil
	}
	rt.agent.Config = rt.cfg
	rt.agent.Workspace = rt.workspace
	return rt.agent.reviewProposedEdit(ctx, preview)
}

func (rt *runtimeState) showBanner() {
	if rt.bannerShown {
		return
	}
	ensureVirtualTerminalProcessing()
	fmt.Fprint(rt.writer, rt.ui.clearScreen())
	fmt.Fprintln(rt.writer, rt.ui.banner(rt.session.Provider, rt.session.Model, rt.session.ID, rt.session.WorkingDir))
	rt.bannerShown = true
}

func (rt *runtimeState) redrawScreen() {
	rt.bannerShown = false
	rt.showBanner()
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Type a task or /help to begin."))
}

func (rt *runtimeState) runREPL() error {
	rt.redrawScreen()
	rt.printAutomationStartupNotice(time.Now())

	for {
		nextTurn := rt.promptTurn + 1
		rt.printTurnSeparator(nextTurn)
		input, err := rt.readInput(rt.ui.prompt(rt.session.Provider, rt.session.Model, rt.mainPromptReasoningEffort()))
		if err != nil {
			if errors.Is(err, ErrPromptCanceled) {
				continue
			}
			if errors.Is(err, io.EOF) {
				fmt.Fprintln(rt.writer)
				return nil
			}
			return err
		}
		line := strings.TrimSpace(input)
		if line == "" {
			continue
		}
		turnStartedAt := time.Now()
		rt.promptTurn = nextTurn
		rt.rememberInputHistory(input)
		if strings.HasPrefix(line, "!") {
			if err := rt.runShell(strings.TrimPrefix(line, "!")); err != nil {
				fmt.Fprintln(rt.writer, rt.ui.errorLine("shell error: "+err.Error()))
			}
			rt.printTurnElapsed(turnStartedAt)
			continue
		}
		if cmd, ok := ParseCommand(line); ok {
			printElapsed := slashCommandShouldPrintTurnElapsed(cmd)
			exit, err := rt.handleCommand(cmd)
			if err != nil {
				rt.printCommandExecutionError(line, err)
			}
			if exit {
				if printElapsed {
					rt.printTurnElapsed(turnStartedAt)
				}
				return nil
			}
			if printElapsed {
				rt.printTurnElapsed(turnStartedAt)
			}
			continue
		}
		if rt.clientErr != nil {
			fmt.Fprintln(rt.writer, rt.ui.errorLine("provider error: "+rt.clientErr.Error()))
			if isMissingKeyError(rt.clientErr) {
				_ = rt.handleAuthError()
			}
			rt.printTurnElapsed(turnStartedAt)
			continue
		}
		ctx := context.Background()
		reply, err := rt.runAgentReply(ctx, input)
		if err != nil {
			if errors.Is(err, ErrEditCanceled) {
				fmt.Fprintln(rt.writer, rt.ui.infoLine("Edit canceled."))
				rt.printTurnElapsed(turnStartedAt)
				continue
			}
			if errors.Is(err, ErrWriteDenied) {
				fmt.Fprintln(rt.writer, rt.ui.infoLine("Write canceled."))
				rt.printTurnElapsed(turnStartedAt)
				continue
			}
			if errors.Is(err, ErrInvalidEditPayload) {
				fmt.Fprintln(rt.writer, rt.ui.warnLine(err.Error()))
				rt.printTurnElapsed(turnStartedAt)
				continue
			}
			for _, line := range rt.formatAssistantError(err) {
				fmt.Fprintln(rt.writer, line)
			}
			if isAuthError(err) {
				_ = rt.handleAuthError()
			}
			rt.printTurnElapsed(turnStartedAt)
			continue
		}
		if strings.TrimSpace(reply) != "" {
			rt.printAssistant(reply)
		}
		rt.printTurnElapsed(turnStartedAt)
	}
}

func (rt *runtimeState) runAgentReply(ctx context.Context, input string) (string, error) {
	return rt.runAgentReplyWithImages(ctx, input, nil)
}

func (rt *runtimeState) runAgentReplyWithExistingCancel(ctx context.Context, input string) (string, error) {
	return rt.runAgentReplyWithImagesManagedCancel(ctx, input, nil, false)
}

func (rt *runtimeState) printTurnSeparator(turn int) {
	fmt.Fprintln(rt.writer)
	fmt.Fprintln(rt.writer, rt.ui.turnSeparator(turn, rt.session.Provider, rt.session.Model))
}

func (rt *runtimeState) printTurnElapsed(startedAt time.Time) {
	if startedAt.IsZero() {
		return
	}
	rt.flushAssistantStream()
	rt.clearThinkingStatus()
	rt.clearThinkingDetails()
	rt.stopThinkingIndicator()
	rt.clearFooterLine()
	rt.writeOutputLines(rt.ui.turnElapsedLine(rt.cfg, time.Since(startedAt)))
}

func slashCommandShouldPrintTurnElapsed(cmd Command) bool {
	switch normalizeSlashCommandName(cmd.Name) {
	case "",
		"exit",
		"quit",
		"help",
		"status",
		"config",
		"trust",
		"version",
		"context",
		"session",
		"sessions",
		"clear",
		"reset",
		"new",
		"tasks",
		"skills",
		"mcp",
		"resources",
		"prompts",
		"reload",
		"hook-reload",
		"hooks",
		"override",
		"override-add",
		"override-clear",
		"memory",
		"mem",
		"mem-search",
		"mem-show",
		"mem-stats",
		"model",
		"effort",
		"provider",
		"profile",
		"permissions",
		"progress-display",
		"selection",
		"selections",
		"use-selection",
		"drop-selection",
		"note-selection",
		"tag-selection",
		"clear-selection",
		"clear-selections",
		"set-max-tool-iterations",
		"set-analysis-models",
		"set-specialist-model",
		"set-auto-verify",
		"locale-auto",
		"set-msbuild-path",
		"clear-msbuild-path",
		"set-cmake-path",
		"clear-cmake-path",
		"set-ctest-path",
		"clear-ctest-path",
		"set-ninja-path",
		"clear-ninja-path",
		"detect-verification-tools":
		return false
	default:
		return true
	}
}

func (rt *runtimeState) printCommandExecutionError(line string, err error) {
	if err == nil {
		return
	}
	if errors.Is(err, ErrPromptCanceled) {
		fmt.Fprintln(rt.writer, rt.ui.infoLine("Canceled."))
		return
	}
	rt.noteCommandError(line, err)
	fmt.Fprintln(rt.writer, rt.ui.errorLine("command error: "+err.Error()))
}

func (rt *runtimeState) formatAssistantError(err error) []string {
	if isUserRequestCancelError(err) {
		return []string{rt.ui.infoLine("Request canceled.")}
	}
	text := strings.TrimSpace(err.Error())
	lines := []string{rt.ui.errorLine("assistant error: " + text)}
	switch {
	case strings.HasPrefix(text, "tool loop limit exceeded"):
		toolSummary, stopReason, recentTurns := parseToolLoopDiagnostics(text)
		if toolSummary != "" {
			lines = append(lines, rt.ui.hintLine("Last tool sequence: "+toolSummary))
		}
		if stopReason != "" {
			lines = append(lines, rt.ui.hintLine("Model stop reason: "+stopReason))
		}
		if recentTurns != "" {
			lines = append(lines, rt.ui.hintLine("Recent tool turns: "+recentTurns))
		}
		lines = append(lines, rt.ui.hintLine("Try tightening the prompt, reducing unnecessary tools, or increasing max_tokens if the model is getting stuck planning."))
	case strings.HasPrefix(text, "stopped after repeated identical tool calls"):
		lines = append(lines, rt.ui.hintLine("The model kept asking for the same tool calls. This usually means it is stuck on the same observation without making progress."))
	case strings.HasPrefix(text, "stopped after repeatedly reading the same file without making progress:"):
		lines = append(lines, rt.ui.hintLine("The model kept re-reading the same file instead of moving to a conclusion or a different source of evidence."))
		lines = append(lines, rt.ui.hintLine("This is usually a model planning failure, not a filesystem issue. Retry with a tighter prompt or a stronger tool-using model if it repeats."))
	case strings.HasPrefix(text, "stopped after repeated tool failure:"):
		lines = append(lines, rt.ui.hintLine("The same tool failure repeated. Fix the failing command or permission issue before retrying."))
		if strings.Contains(text, ErrInvalidPatchFormat.Error()) {
			lines = append(lines, rt.ui.hintLine("apply_patch must send raw patch text, not prose or JSON."))
			lines = append(lines, rt.ui.hintLine("Required header: *** Begin Patch"))
			lines = append(lines, rt.ui.hintLine("Required footer: *** End Patch"))
			lines = append(lines, rt.ui.hintLine("Use sections like *** Update File:, *** Add File:, or *** Delete File: between them."))
		}
		if strings.Contains(text, ErrEditTargetMismatch.Error()) {
			rejectedPath := parseRejectedEditTargetPath(text)
			lines = append(lines, rt.ui.hintLine("Active workspace root: "+rt.session.WorkingDir))
			if rejectedPath != "" {
				lines = append(lines, rt.ui.hintLine("Rejected target path: "+rejectedPath))
			}
			if strings.Contains(strings.ToLower(text), "editable ownership") {
				lines = append(lines, rt.ui.hintLine("The routed specialist owns only a limited file scope. Pick a path that matches its ownership globs, or reassign the node with /specialists assign before editing."))
			}
			lines = append(lines, rt.ui.hintLine("Re-read the file from the exact same path before editing again, and avoid crossing into a different worktree."))
		}
	case strings.Contains(text, "token limit"):
		_, stopReason, _ := parseToolLoopDiagnostics(text)
		if stopReason != "" {
			lines = append(lines, rt.ui.hintLine("Model stop reason: "+stopReason))
		}
		lines = append(lines, rt.ui.hintLine("Increase max_tokens, reduce prompt bloat, or ask for a shorter intermediate answer."))
	case strings.Contains(text, "does not support tool use"):
		lines = append(lines, rt.ui.hintLine("This model endpoint cannot use Kernforge tools, so it cannot reliably inspect files, run commands, or apply edits."))
		lines = append(lines, rt.ui.hintLine("Switch to a tool-capable model for review/fix tasks, or ask for a no-tools answer if you only want a best-effort opinion from attached context."))
		lines = rt.appendIrrecoverableModelAlternatives(lines)
	case strings.Contains(text, "model returned an empty response"):
		lines = append(lines, rt.ui.hintLine("The provider returned no visible assistant text and no tool calls. This is a model/provider response failure, not a filesystem or shell failure."))
		if strings.Contains(text, "stream_empty_fallback_empty_after_stream_retry") {
			lines = append(lines, rt.ui.hintLine("The streamed reply was empty, and Kernforge retried once without streaming but that fallback reply was also empty."))
		}
		if strings.Contains(text, "after_tool=true") {
			lines = append(lines, rt.ui.hintLine("This happened after at least one successful tool result, so the model likely stalled on the follow-up turn after seeing tool output."))
		}
		if strings.Contains(text, "provider=openrouter") {
			lines = append(lines, rt.ui.hintLine("Try a different OpenRouter model for tool-heavy work, or retry with a stronger tool-using model if this one keeps returning blank follow-up turns."))
		}
		lines = rt.appendIrrecoverableModelAlternatives(lines)
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(text, "context deadline exceeded") || strings.Contains(text, "client.timeout exceeded"):
		lines = append(lines, rt.ui.hintLine("The model request timed out before a usable response arrived. Retry the request, reduce prompt/tool churn, or increase the request timeout if your provider is slow."))
	}
	return lines
}

func isUserRequestCancelError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrRequestCanceled) {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(err.Error()), ErrRequestCanceled.Error())
}

func (rt *runtimeState) appendIrrecoverableModelAlternatives(lines []string) []string {
	lines = append(lines, rt.ui.hintLine("Alternative 1: retry the same prompt with a different tool-capable model or provider."))
	lines = append(lines, rt.ui.hintLine("Alternative 2: ask for a no-tools answer that returns the research or document content inline instead of writing files."))
	lines = append(lines, rt.ui.hintLine("Alternative 3: split the task into smaller steps, then retry the file-writing or edit step separately."))
	return lines
}

func parseRejectedEditTargetPath(text string) string {
	markers := []string{
		"outside the active workspace root: ",
		"search text not found in ",
	}
	for _, marker := range markers {
		index := strings.Index(text, marker)
		if index < 0 {
			continue
		}
		path := strings.TrimSpace(text[index+len(marker):])
		path = strings.TrimSuffix(path, ")")
		return path
	}
	return ""
}

func parseToolLoopDiagnostics(text string) (string, string, string) {
	start := strings.Index(text, "(")
	end := strings.LastIndex(text, ")")
	if start < 0 || end <= start {
		return "", "", ""
	}
	inside := strings.TrimSpace(text[start+1 : end])
	if inside == "" {
		return "", "", ""
	}
	var toolSummary string
	var stopReason string
	var recentTurns string
	for _, part := range strings.Split(inside, ";") {
		item := strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(item, "last_tools="):
			toolSummary = strings.TrimSpace(strings.TrimPrefix(item, "last_tools="))
		case strings.HasPrefix(item, "stop_reason="):
			stopReason = strings.TrimSpace(strings.TrimPrefix(item, "stop_reason="))
		case strings.HasPrefix(item, "recent_turns="):
			recentTurns = strings.TrimSpace(strings.TrimPrefix(item, "recent_turns="))
		}
	}
	return toolSummary, stopReason, recentTurns
}

func (rt *runtimeState) runAgentReplyWithImages(ctx context.Context, input string, images []MessageImage) (string, error) {
	return rt.runAgentReplyWithImagesManagedCancel(ctx, input, images, true)
}

func (rt *runtimeState) runAgentReplyWithImagesManagedCancel(ctx context.Context, input string, images []MessageImage, installCancelWatcher bool) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if verdict, err := rt.runHook(ctx, HookUserPromptSubmit, userPromptSubmitHookPayload(input)); err != nil {
		return "", err
	} else if len(verdict.ContextAdds) > 0 {
		input = strings.TrimSpace(input) + "\n\nAdditional hook guidance:\n- " + strings.Join(verdict.ContextAdds, "\n- ")
	}
	requestCtx := ctx
	cancel := func() {}
	installCancelWatcher = installCancelWatcher && rt.shouldInstallRequestCancelWatcher()
	if installCancelWatcher {
		requestCtx, cancel = context.WithCancel(ctx)
		defer cancel()
	}
	rt.resetAssistantDedup()
	rt.resetAssistantStream()
	rt.allowThinkingIndicator()
	rt.clearThinkingStatus()
	rt.clearThinkingDetails()
	rt.armAutoCheckpoint()
	defer rt.clearAutoCheckpoint()
	defer rt.allowThinkingIndicator()
	defer rt.clearThinkingStatus()
	defer rt.clearThinkingDetails()

	if installCancelWatcher {
		rt.clearRequestCancelState()
		defer rt.clearRequestCancelState()
		cancelRequest := func() {
			rt.beginRequestCancel()
			cancel()
		}
		stopEscapeWatcher := startEscapeWatcher(cancelRequest, rt.shouldHonorRequestCancel, rt.confirmRequestCancel)
		defer stopEscapeWatcher()
	}

	if handled, err := rt.maybeHandleNaturalLanguageReview(requestCtx, input, images); handled {
		if err != nil && installCancelWatcher && requestCtx.Err() == context.Canceled && ctx.Err() == nil {
			rt.noteRecentRequestCancel()
			return "", ErrRequestCanceled
		}
		return "", err
	}

	rt.startThinkingIndicator()
	defer rt.stopThinkingIndicator()
	defer rt.finishAssistantStream()

	reply, err := rt.agent.ReplyWithImages(requestCtx, input, images)
	if err != nil {
		if installCancelWatcher && requestCtx.Err() == context.Canceled && ctx.Err() == nil {
			rt.noteRecentRequestCancel()
			return "", ErrRequestCanceled
		}
		return "", err
	}
	return reply, nil
}

func (rt *runtimeState) startThinkingIndicator() {
	rt.thinkingMu.Lock()
	if rt.thinkingStop != nil {
		rt.thinkingMu.Unlock()
		return
	}
	rt.thinkingMu.Unlock()
	if rt.isThinkingSuppressed() {
		return
	}

	frames := []string{"-", "\\", "|", "/"}
	stop := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once

	stopFn := func() {
		once.Do(func() {
			close(stop)
			<-done
		})
	}

	rt.thinkingMu.Lock()
	rt.thinkingStop = stopFn
	rt.thinkingStartedAt = time.Now()
	rt.thinkingMu.Unlock()

	go func() {
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()

		lastRendered := ""
		render := func(frame string) {
			rt.thinkingMu.Lock()
			startedAt := rt.thinkingStartedAt
			rt.thinkingMu.Unlock()
			elapsed := time.Since(startedAt)
			status := rt.currentThinkingStatus(elapsed)
			renderFrame := frame
			if !shouldAnimateThinkingStatus(status) {
				renderFrame = "-"
			}
			lines := []string{rt.ui.thinkingLine(renderFrame, elapsed, status)}
			lines = append(lines, rt.currentThinkingDetails()...)
			rendered := strings.Join(lines, "\n")
			if rendered == lastRendered {
				return
			}
			rt.renderFooterText(rendered)
			lastRendered = rendered
		}

		index := 0
		render(frames[index])
		for {
			select {
			case <-stop:
				rt.clearFooterLine()
				close(done)
				return
			case <-ticker.C:
				index = (index + 1) % len(frames)
				render(frames[index])
			}
		}
	}()
}

func (rt *runtimeState) stopThinkingIndicator() {
	rt.thinkingMu.Lock()
	stop := rt.thinkingStop
	rt.thinkingStop = nil
	rt.thinkingMu.Unlock()
	if stop != nil {
		stop()
	}
}

func (rt *runtimeState) resetAssistantStream() {
	rt.streamMu.Lock()
	rt.streamingAssistant = false
	rt.streamedAssistantText.Reset()
	rt.pendingAssistantSpacing = ""
	rt.assistantStreamInFence = false
	rt.assistantStreamLine = ""
	rt.streamMu.Unlock()
}

func (rt *runtimeState) suppressThinkingIndicator() {
	rt.suppressThinkingMu.Lock()
	rt.suppressThinking = true
	rt.suppressThinkingMu.Unlock()
}

func (rt *runtimeState) allowThinkingIndicator() {
	rt.suppressThinkingMu.Lock()
	rt.suppressThinking = false
	rt.suppressThinkingMu.Unlock()
}

func (rt *runtimeState) isThinkingSuppressed() bool {
	rt.suppressThinkingMu.Lock()
	defer rt.suppressThinkingMu.Unlock()
	return rt.suppressThinking
}

func (rt *runtimeState) appendAssistantStream(text string) {
	if text == "" {
		return
	}

	rt.streamMu.Lock()
	defer rt.streamMu.Unlock()

	if !rt.streamingAssistant {
		rt.pendingAssistantSpacing = ""
		text = strings.TrimLeftFunc(text, unicode.IsSpace)
		if text == "" {
			return
		}
	}
	if rt.streamingAssistant && isAssistantBlankLineChunk(text) {
		rt.pendingAssistantSpacing += text
		if countLineBreaks(rt.pendingAssistantSpacing) >= 3 {
			rt.writeOutput("\n")
			rt.streamingAssistant = false
			if normalized := normalizeAssistantDisplayText(rt.streamedAssistantText.String()); normalized != "" {
				rt.lastAssistantMu.Lock()
				rt.lastAssistantPrinted = normalized
				rt.lastAssistantMu.Unlock()
			}
			rt.streamedAssistantText.Reset()
			rt.pendingAssistantSpacing = ""
			rt.assistantStreamInFence = false
			rt.assistantStreamLine = ""
			rt.setThinkingStatus(localizedText(rt.cfg, "Working ...", "작업 중 ..."))
			rt.allowThinkingIndicator()
			rt.startThinkingIndicator()
		}
		return
	}
	if rt.pendingAssistantSpacing != "" {
		text = rt.pendingAssistantSpacing + text
		rt.pendingAssistantSpacing = ""
	}
	rt.clearThinkingStatus()
	rt.clearThinkingDetails()
	rt.suppressThinkingIndicator()
	if !rt.streamingAssistant {
		rt.stopThinkingIndicator()
		rt.writeOutput(rt.ui.assistantHeader() + "\n")
		rt.streamingAssistant = true
	}
	text = formatAssistantStreamDelta(rt.streamedAssistantText.String(), text)
	if text == "" {
		return
	}
	rt.streamedAssistantText.WriteString(text)
	rt.writeOutput(rt.ui.renderAssistantStreamDelta(text, &rt.assistantStreamInFence, &rt.assistantStreamLine))
}

func (rt *runtimeState) finishAssistantStream() {
	rt.streamMu.Lock()
	defer rt.streamMu.Unlock()
	rt.pendingAssistantSpacing = ""

	if !rt.streamingAssistant {
		return
	}
	rt.writeOutput("\n")
	rt.streamingAssistant = false
	if normalized := normalizeAssistantDisplayText(rt.streamedAssistantText.String()); normalized != "" {
		rt.lastAssistantMu.Lock()
		rt.lastAssistantPrinted = normalized
		rt.lastAssistantMu.Unlock()
	}
	rt.streamedAssistantText.Reset()
	rt.assistantStreamInFence = false
	rt.assistantStreamLine = ""
}

func (rt *runtimeState) flushAssistantStream() {
	rt.finishAssistantStream()
}

func (rt *runtimeState) printPersistentWhileThinking(lines ...string) {
	rt.flushAssistantStream()
	rt.clearThinkingDetails()
	resumeThinking := rt.suspendThinkingIndicator()
	defer resumeThinking()
	rt.outputMu.Lock()
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			fmt.Fprintln(rt.writer)
			continue
		}
		fmt.Fprintln(rt.writer, rt.ui.progressLine(line))
	}
	rt.outputMu.Unlock()
}

func (rt *runtimeState) printWhileThinking(lines ...string) {
	rt.printPersistentWhileThinking(lines...)
}

func (rt *runtimeState) showTransientProgressFooter(text string) bool {
	if !rt.interactive {
		return false
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	rt.flushAssistantStream()
	rt.clearThinkingDetails()
	rt.allowThinkingIndicator()
	rt.setThinkingStatus(compactThinkingStatus(rt.cfg, trimmed))
	rt.startThinkingIndicator()
	return true
}

func (rt *runtimeState) showTransientPanelWhileThinking(lines ...string) bool {
	if !rt.interactive {
		return false
	}
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cleaned = append(cleaned, line)
	}
	if len(cleaned) == 0 {
		return false
	}
	rt.flushAssistantStream()
	rt.allowThinkingIndicator()
	rt.clearThinkingStatus()
	rt.setThinkingDetails(cleaned...)
	rt.startThinkingIndicator()
	return true
}

func (rt *runtimeState) showTransientWhileThinking(lines ...string) bool {
	return rt.showTransientPanelWhileThinking(lines...)
}

func (rt *runtimeState) printProgressMessage(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	rt.printProgressEvent(ProgressEvent{
		Kind:    classifyProgressKind(text),
		Message: text,
	})
}

func (rt *runtimeState) printProgressEvent(event ProgressEvent) {
	text := strings.TrimSpace(formatProgressEventMessage(rt.cfg, event))
	if text == "" {
		return
	}
	kind := progressEventActivityKind(event, text)
	rt.setThinkingStatus(compactThinkingStatus(rt.cfg, text))
	if rt.shouldPersistProgressEvent(event, text) {
		rt.printPersistentWhileThinking(rt.ui.activityLine(kind, text))
		return
	}
	if rt.showTransientProgressFooter(text) {
		return
	}
	rt.printWhileThinking(rt.ui.activityLine(kind, text))
}

func progressEventActivityKind(event ProgressEvent, text string) string {
	switch strings.TrimSpace(event.Kind) {
	case progressKindMemoryContext:
		return "memory"
	case progressKindModelRequestStart, progressKindModelRequestWait, progressKindModelRequestDone,
		progressKindModelRouteWait, progressKindModelRouteAcquired,
		progressKindModelStreamToolCall, progressKindModelStreamToolArgs, progressKindModelStreamToolReady,
		progressKindProviderRetry:
		return "model"
	case progressKindToolStarted, progressKindToolCompleted, progressKindToolFailed:
		return "tool"
	default:
		return classifyProgressKind(text)
	}
}

func (rt *runtimeState) shouldPersistProgressEvent(event ProgressEvent, text string) bool {
	if rt == nil || !rt.interactive {
		return true
	}
	mode := configProgressDisplay(rt.cfg)
	switch mode {
	case "compact":
		return false
	case "stream":
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	if strings.HasPrefix(lower, "run_shell output:") ||
		strings.Contains(lower, "run_shell still running after") ||
		strings.Contains(lower, "shell still running after") {
		return false
	}
	switch strings.TrimSpace(event.Kind) {
	case progressKindToolStarted, progressKindToolCompleted, progressKindToolFailed,
		progressKindModelStreamToolCall, progressKindModelStreamToolReady,
		progressKindProviderRetry,
		progressKindMemoryContext:
		return true
	case progressKindModelRouteAcquired:
		return event.Elapsed >= time.Second
	case progressKindModelRequestStart:
		return true
	case progressKindModelRequestWait:
		return event.Elapsed >= 5*time.Second
	case progressKindModelRequestDone:
		return strings.EqualFold(event.Status, "failed") || event.Elapsed >= 5*time.Second
	}
	switch classifyProgressKind(text) {
	case "tool", "verify":
		return true
	case "edit":
		return strings.Contains(lower, "patch applied") ||
			strings.Contains(lower, "file updated") ||
			strings.Contains(lower, "replacement applied") ||
			strings.Contains(lower, "edit saved")
	case "model":
		return strings.Contains(lower, "retry") ||
			strings.Contains(lower, "timed out") ||
			strings.Contains(lower, "tool call")
	default:
		return false
	}
}

func (rt *runtimeState) writeOutput(text string) {
	rt.outputMu.Lock()
	defer rt.outputMu.Unlock()
	ensureVirtualTerminalProcessing()
	fmt.Fprint(rt.writer, text)
}

func (rt *runtimeState) clearFooterLineLocked() {
	if !rt.footerVisible {
		return
	}
	ensureVirtualTerminalProcessing()
	if rt.footerLineCount == 1 {
		width := visibleLen(rt.footerText)
		if width > 0 {
			fmt.Fprint(rt.writer, "\r"+strings.Repeat(" ", width)+"\r")
		} else {
			fmt.Fprint(rt.writer, "\r")
		}
		rt.footerVisible = false
		rt.footerLineCount = 0
		rt.footerText = ""
		return
	}
	if rt.footerLineCount > 1 {
		fmt.Fprintf(rt.writer, "\x1b[%dA", rt.footerLineCount-1)
	}
	fmt.Fprint(rt.writer, "\r\x1b[J")
	rt.footerVisible = false
	rt.footerLineCount = 0
	rt.footerText = ""
}

func footerDisplayLineCount(text string) int {
	if text == "" {
		return 0
	}
	termW := terminalWidth()
	if termW <= 0 {
		termW = 120
	}
	total := 0
	for _, line := range strings.Split(text, "\n") {
		width := visibleLen(line)
		if width <= 0 {
			total++
			continue
		}
		total += ((width - 1) / termW) + 1
	}
	return total
}

func (rt *runtimeState) renderFooterTextLocked(text string) {
	if strings.TrimSpace(text) == "" {
		rt.clearFooterLineLocked()
		return
	}
	ensureVirtualTerminalProcessing()
	nextLineCount := footerDisplayLineCount(text)
	if rt.footerVisible && rt.footerLineCount == 1 && nextLineCount == 1 {
		oldWidth := visibleLen(rt.footerText)
		newWidth := visibleLen(text)
		fmt.Fprint(rt.writer, "\r")
		fmt.Fprint(rt.writer, text)
		if newWidth < oldWidth {
			fmt.Fprint(rt.writer, strings.Repeat(" ", oldWidth-newWidth))
		}
		rt.footerVisible = true
		rt.footerLineCount = 1
		rt.footerText = text
		return
	}
	rt.clearFooterLineLocked()
	fmt.Fprint(rt.writer, text)
	rt.footerVisible = true
	rt.footerLineCount = nextLineCount
	rt.footerText = text
}

func (rt *runtimeState) clearFooterLine() {
	rt.outputMu.Lock()
	defer rt.outputMu.Unlock()
	rt.clearFooterLineLocked()
}

func (rt *runtimeState) renderFooterText(text string) {
	rt.outputMu.Lock()
	defer rt.outputMu.Unlock()
	rt.renderFooterTextLocked(text)
}

func (rt *runtimeState) renderFooterLine(text string) {
	rt.renderFooterText(text)
}

func (rt *runtimeState) writeOutputLines(lines ...string) {
	rt.outputMu.Lock()
	defer rt.outputMu.Unlock()
	ensureVirtualTerminalProcessing()
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			fmt.Fprintln(rt.writer)
			continue
		}
		fmt.Fprintln(rt.writer, line)
	}
}

func (rt *runtimeState) setThinkingStatus(text string) {
	rt.thinkingStatusMu.Lock()
	rt.thinkingStatusOverride = strings.TrimSpace(text)
	rt.thinkingStatusMu.Unlock()
}

// printLegacyConfigMigrationNotices surfaces config values that were silently
// rewritten because they exactly matched a previous KernForge hard-coded
// default. Shown once per run only when migrations actually happened.
func (rt *runtimeState) printLegacyConfigMigrationNotices(notices []LegacyDefaultMigration) {
	if rt == nil || len(notices) == 0 {
		return
	}
	fmt.Fprintln(rt.writer, rt.ui.infoLine("Migrated legacy KernForge config defaults to current values:"))
	for _, n := range notices {
		fmt.Fprintln(rt.writer, rt.ui.infoLine(fmt.Sprintf("  %s: %s -> %s (%s)", n.Field, n.OldValue, n.NewValue, n.Reason)))
	}
	fmt.Fprintln(rt.writer, rt.ui.infoLine("To pin a different value, set it explicitly in your .kernforge/config.json."))
}

func (rt *runtimeState) printBypassHookTrustNotice() {
	if rt == nil || rt.writer == nil || !configBypassHookTrust(rt.cfg) {
		return
	}
	fmt.Fprintln(rt.writer, rt.ui.warnLine("--dangerously-bypass-hook-trust is enabled. Project-local hooks may run without saved trust for this invocation."))
}

// resetThinkingTimer rebases the thinking spinner's elapsed clock to "now",
// so a phase boundary (e.g. plan-review starting/ending, recovery beginning)
// reports its own duration rather than carrying over time from a previous
// phase or a stale goroutine that survived a turn boundary.
func (rt *runtimeState) resetThinkingTimer() {
	if rt == nil {
		return
	}
	rt.thinkingMu.Lock()
	if rt.thinkingStop != nil {
		rt.thinkingStartedAt = time.Now()
	}
	rt.thinkingMu.Unlock()
}

func (rt *runtimeState) clearThinkingStatus() {
	rt.thinkingStatusMu.Lock()
	rt.thinkingStatusOverride = ""
	rt.thinkingStatusMu.Unlock()
}

func (rt *runtimeState) setThinkingDetails(lines ...string) {
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		filtered = append(filtered, line)
	}
	rt.thinkingDetailMu.Lock()
	rt.thinkingDetailLines = filtered
	rt.thinkingDetailMu.Unlock()
}

func (rt *runtimeState) clearThinkingDetails() {
	rt.thinkingDetailMu.Lock()
	rt.thinkingDetailLines = nil
	rt.thinkingDetailMu.Unlock()
}

func (rt *runtimeState) currentThinkingDetails() []string {
	rt.thinkingDetailMu.Lock()
	defer rt.thinkingDetailMu.Unlock()
	return append([]string(nil), rt.thinkingDetailLines...)
}

func (rt *runtimeState) beginRequestCancel() {
	rt.requestCancelMu.Lock()
	rt.requestCancelPending = true
	rt.requestCancelMu.Unlock()
}

func (rt *runtimeState) clearRequestCancelState() {
	rt.requestCancelMu.Lock()
	rt.requestCancelPending = false
	rt.requestCancelMu.Unlock()
}

func (rt *runtimeState) currentThinkingStatus(elapsed time.Duration) string {
	rt.requestCancelMu.Lock()
	cancelPending := rt.requestCancelPending
	rt.requestCancelMu.Unlock()
	if cancelPending {
		return localizedText(rt.cfg, "Canceling current request...", "취소하는 중 ...")
	}
	rt.thinkingStatusMu.Lock()
	override := rt.thinkingStatusOverride
	rt.thinkingStatusMu.Unlock()
	if override != "" {
		return override
	}
	if elapsed >= 45*time.Second {
		return localizedText(rt.cfg, "Thinking ... still preparing the response.", "생각 중 ... 아직 답변을 정리하고 있어요.")
	}
	if elapsed >= 15*time.Second {
		return localizedText(rt.cfg, "Thinking ...", "생각 중 ...")
	}
	return localizedText(rt.cfg, "Thinking ...", "생각 중 ...")
}

func compactThinkingStatus(cfg Config, text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}

	lower := strings.ToLower(trimmed)
	switch {
	case strings.Contains(lower, "cancel"):
		return localizedText(cfg, "Canceling current request...", "취소하는 중 ...")
	case strings.HasPrefix(lower, "using read_file"):
		return trimmed
	case strings.HasPrefix(lower, "using grep"):
		return trimmed
	case strings.HasPrefix(lower, "using list_files"):
		return trimmed
	case strings.HasPrefix(lower, "using git_"):
		return trimmed
	case strings.HasPrefix(lower, "using mcp__"):
		return trimmed
	case strings.HasPrefix(lower, "using "):
		return trimmed
	case strings.HasPrefix(lower, "running shell:"):
		return trimmed
	case strings.HasPrefix(lower, "writing "):
		return localizedText(cfg, "Applying edit...", "수정 적용 중 ...")
	case strings.HasPrefix(lower, "saved "):
		return localizedText(cfg, "Edit saved.", "수정 저장 완료.")
	case strings.HasPrefix(lower, "running post-edit hooks"):
		return localizedText(cfg, "Running post-edit hooks...", "후처리 hook 실행 중 ...")
	case strings.HasPrefix(lower, "post-edit hooks finished"):
		return localizedText(cfg, "Post-edit hooks finished.", "후처리 hook 실행 완료.")
	case strings.HasPrefix(lower, "creating automatic checkpoint"):
		return localizedText(cfg, "Creating checkpoint...", "체크포인트 생성 중 ...")
	case strings.HasPrefix(lower, "patch applied"):
		return localizedText(cfg, "Patch applied.", "패치 적용 완료.")
	case strings.HasPrefix(lower, "file updated"):
		return localizedText(cfg, "File updated.", "파일 갱신 완료.")
	case strings.HasPrefix(lower, "replacement applied"):
		return localizedText(cfg, "Replacement applied.", "치환 적용 완료.")
	case strings.Contains(lower, "checking follow-up"):
		return localizedText(cfg, "Checking follow-up steps...", "후속 단계 확인 중 ...")
	case strings.Contains(lower, "running automatic pre-write review"):
		return localizedText(cfg, "Reviewing proposed edit...", "제안된 수정 리뷰 중 ...")
	case strings.Contains(lower, "automatic pre-write review found blockers"):
		return localizedText(cfg, "Review blocked proposed edit.", "리뷰가 제안 수정을 차단했습니다.")
	case strings.Contains(lower, "automatic pre-write review completed"):
		return localizedText(cfg, "Proposed edit reviewed.", "제안 수정 리뷰 완료.")
	case strings.Contains(lower, "automatic pre-write review failed"):
		return localizedText(cfg, "Pre-write review failed.", "쓰기 전 리뷰 실패.")
	case strings.Contains(lower, "running automatic post-change review"):
		return localizedText(cfg, "Running review...", "자동 리뷰 실행 중 ...")
	case strings.Contains(lower, "automatic post-change review found blockers"):
		return localizedText(cfg, "Review found blockers.", "리뷰 차단 항목 발견.")
	case strings.Contains(lower, "automatic post-change review completed"):
		return localizedText(cfg, "Review completed.", "리뷰 완료.")
	case strings.Contains(lower, "automatic post-change review failed"):
		return localizedText(cfg, "Review failed.", "리뷰 실패.")
	case strings.Contains(lower, "automatic verification failed"):
		return localizedText(cfg, "Verification failed.", "검증 실패.")
	case strings.Contains(lower, "automatic verification finished"):
		return localizedText(cfg, "Verification finished.", "검증 완료.")
	case strings.Contains(lower, "waiting for the model to summarize"):
		return localizedText(cfg, "Finalizing reply...", "답변 정리 중 ...")
	}

	return truncateDisplayText(trimmed, 48)
}

func classifyProgressKind(text string) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	switch {
	case lower == "":
		return "info"
	case strings.Contains(lower, "read_file"),
		strings.Contains(lower, "grep"),
		strings.Contains(lower, "list_files"),
		strings.Contains(lower, "run_shell"),
		strings.Contains(lower, "git_"),
		strings.HasPrefix(lower, "using "):
		return "tool"
	case strings.Contains(lower, "verification"),
		strings.Contains(lower, "verify"):
		return "verify"
	case strings.Contains(lower, "apply_patch"),
		strings.Contains(lower, "write_file"),
		strings.Contains(lower, "replace_in_file"),
		strings.Contains(lower, "edit saved"),
		strings.Contains(lower, "edit applied"),
		strings.Contains(lower, "writing "),
		strings.Contains(lower, "saved "),
		strings.Contains(lower, "patch applied"),
		strings.Contains(lower, "file updated"),
		strings.Contains(lower, "replacement applied"),
		strings.Contains(lower, "post-edit"):
		return "edit"
	case strings.Contains(lower, "checkpoint"),
		strings.Contains(lower, "follow-up steps"):
		return "edit"
	case strings.Contains(lower, "analysis"):
		return "analysis"
	case strings.Contains(lower, "memory"),
		strings.Contains(lower, "메모리"):
		return "memory"
	case strings.Contains(lower, "model"),
		strings.Contains(lower, "retrying"),
		strings.Contains(lower, "timeout"):
		return "model"
	default:
		return "info"
	}
}

func normalizeAssistantDisplayText(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	normalized := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return ' '
		case r == '`':
			return -1
		case unicode.IsPunct(r):
			return ' '
		default:
			return unicode.ToLower(r)
		}
	}, trimmed)
	return strings.Join(strings.Fields(normalized), " ")
}

func normalizeAssistantStreamPreambleBoundary(existing string, incoming string) string {
	if strings.TrimSpace(existing) == "" || incoming == "" {
		return incoming
	}
	if strings.HasPrefix(incoming, "\n") || strings.HasPrefix(incoming, "\r") {
		return incoming
	}
	lowerIncoming := strings.ToLower(incoming)
	if !strings.HasPrefix(lowerIncoming, "let me ") &&
		!strings.HasPrefix(lowerIncoming, "now let me ") &&
		!strings.HasPrefix(lowerIncoming, "now i ") &&
		!strings.HasPrefix(lowerIncoming, "i'll ") &&
		!strings.HasPrefix(lowerIncoming, "i will ") &&
		!strings.HasPrefix(lowerIncoming, "i need to ") &&
		!strings.HasPrefix(lowerIncoming, "first, ") {
		return incoming
	}

	last := rune(0)
	for _, r := range existing {
		last = r
	}
	switch last {
	case '.', ':', '!', '?', ')':
		return "\n" + incoming
	default:
		return incoming
	}
}

func formatAssistantStreamDelta(existing string, incoming string) string {
	if incoming == "" {
		return ""
	}
	incoming = normalizeAssistantStreamPreambleBoundary(existing, incoming)
	incoming = splitAssistantPreambleBoundaries(incoming)

	formattedExisting := formatAssistantText(existing)
	formattedCombined := formatAssistantText(existing + incoming)
	if strings.HasPrefix(formattedCombined, formattedExisting) {
		delta := formattedCombined[len(formattedExisting):]
		return delta + strings.Repeat("\n", countTrailingLineBreaks(incoming))
	}
	return incoming
}

func splitAssistantPreambleBoundaries(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	return assistantFollowOnPreamblePattern.ReplaceAllString(text, "$1\n$2")
}

func isAssistantBlankLineChunk(text string) bool {
	return strings.TrimSpace(text) == "" && strings.ContainsAny(text, "\r\n")
}

func countLineBreaks(text string) int {
	count := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '\n':
			count++
		case '\r':
			if i+1 >= len(text) || text[i+1] != '\n' {
				count++
			}
		}
	}
	return count
}

func countTrailingLineBreaks(text string) int {
	count := 0
	for i := len(text) - 1; i >= 0; i-- {
		switch text[i] {
		case '\n':
			count++
		case '\r':
			if i+1 >= len(text) || text[i+1] != '\n' {
				count++
			}
		default:
			return count
		}
	}
	return count
}

func (rt *runtimeState) resetAssistantDedup() {
	rt.lastAssistantMu.Lock()
	rt.lastAssistantPrinted = ""
	rt.lastAssistantMu.Unlock()
}

func (rt *runtimeState) shouldPrintAssistant(text string) bool {
	normalized := normalizeAssistantDisplayText(text)
	if normalized == "" {
		return false
	}
	rt.lastAssistantMu.Lock()
	defer rt.lastAssistantMu.Unlock()
	if normalized == rt.lastAssistantPrinted {
		return false
	}
	rt.lastAssistantPrinted = normalized
	return true
}

func (rt *runtimeState) printAssistant(text string) {
	if !rt.shouldPrintAssistant(text) {
		return
	}
	rt.flushAssistantStream()
	rt.clearThinkingStatus()
	rt.clearThinkingDetails()
	rt.suppressThinkingIndicator()
	rt.stopThinkingIndicator()
	rt.outputMu.Lock()
	defer rt.outputMu.Unlock()
	fmt.Fprintln(rt.writer, rt.ui.assistant(text))
}

func (rt *runtimeState) printAssistantWhileThinking(text string) {
	if !rt.shouldPrintAssistant(text) {
		return
	}
	if rt.showTransientPanelWhileThinking(rt.ui.activityLine("next", text)) {
		return
	}
	rt.clearThinkingStatus()
	rt.clearThinkingDetails()
	rt.printPersistentWhileThinking(rt.ui.activityLine("next", text))
}

func (rt *runtimeState) shouldHonorRequestCancel() bool {
	rt.requestCancelMu.Lock()
	defer rt.requestCancelMu.Unlock()
	return rt.requestCancelPauses == 0
}

func (rt *runtimeState) shouldInstallRequestCancelWatcher() bool {
	return rt != nil && rt.interactive
}

func (rt *runtimeState) confirmRequestCancel() bool {
	if !rt.interactive {
		return false
	}
	var (
		allowed bool
		err     error
	)
	rt.withRequestCancelSuspended(func() {
		allowed, err = rt.confirm("Cancel current request?")
	})
	if err != nil {
		if errors.Is(err, ErrPromptCanceled) {
			rt.printPersistentWhileThinking(rt.ui.infoLine("Request cancel dismissed."))
			return false
		}
		rt.printPersistentWhileThinking(rt.ui.warnLine("request cancel confirmation failed: " + err.Error()))
		return false
	}
	return allowed
}

func (rt *runtimeState) noteRecentRequestCancel() {
	rt.requestCancelMu.Lock()
	rt.requestCancelIgnoreUntil = time.Now().Add(350 * time.Millisecond)
	rt.requestCancelMu.Unlock()
}

func (rt *runtimeState) shouldIgnorePromptEscape() bool {
	rt.requestCancelMu.Lock()
	defer rt.requestCancelMu.Unlock()
	if rt.requestCancelIgnoreUntil.IsZero() {
		return false
	}
	if time.Now().After(rt.requestCancelIgnoreUntil) {
		rt.requestCancelIgnoreUntil = time.Time{}
		return false
	}
	return true
}

func (rt *runtimeState) consumeRecentRequestCancel() bool {
	rt.requestCancelMu.Lock()
	defer rt.requestCancelMu.Unlock()
	if rt.requestCancelIgnoreUntil.IsZero() {
		return false
	}
	active := time.Now().Before(rt.requestCancelIgnoreUntil)
	rt.requestCancelIgnoreUntil = time.Time{}
	return active
}

func (rt *runtimeState) suspendRequestCancel() func() {
	rt.requestCancelMu.Lock()
	rt.requestCancelPauses++
	rt.requestCancelMu.Unlock()

	return func() {
		rt.requestCancelMu.Lock()
		if rt.requestCancelPauses > 0 {
			rt.requestCancelPauses--
		}
		rt.requestCancelMu.Unlock()
	}
}

func (rt *runtimeState) withRequestCancelSuspended(fn func()) {
	resumeRequestCancel := rt.suspendRequestCancel()
	defer resumeRequestCancel()
	fn()
}

func (rt *runtimeState) suspendThinkingIndicator() func() {
	rt.thinkingMu.Lock()
	stop := rt.thinkingStop
	active := stop != nil
	rt.thinkingStop = nil
	rt.thinkingMu.Unlock()
	if stop != nil {
		stop()
	}
	return func() {
		if active {
			rt.startThinkingIndicator()
		}
	}
}

func (rt *runtimeState) runShell(command string) error {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return nil
	}
	if handled, err := rt.runBuiltinShell(trimmed); handled {
		return err
	}
	out, err := NewRunShellTool(rt.workspace).Execute(context.Background(), map[string]any{
		"command": trimmed,
	})
	if strings.TrimSpace(out) != "" {
		fmt.Fprintln(rt.writer, rt.ui.shell(out))
	}
	rt.noteLocalShellCommand(trimmed, out, err)
	return err
}

func (rt *runtimeState) runBuiltinShell(command string) (bool, error) {
	lower := strings.ToLower(command)
	switch {
	case lower == "cd" || strings.HasPrefix(lower, "cd "):
		return true, rt.changeDirectory(strings.TrimSpace(command[2:]))
	case lower == "cls" || lower == "clear":
		rt.redrawScreen()
		return true, nil
	case lower == "ls" || strings.HasPrefix(lower, "ls "):
		return true, rt.listDirectory(strings.TrimSpace(command[2:]))
	case lower == "dir" || strings.HasPrefix(lower, "dir "):
		return true, rt.listDirectory(strings.TrimSpace(command[3:]))
	case lower == "pwd":
		fmt.Fprintln(rt.writer, rt.ui.shell(rt.workspace.Root))
		return true, nil
	}
	return false, nil
}

func (rt *runtimeState) changeDirectory(pathArg string) error {
	target := pathArg
	if strings.TrimSpace(target) == "" {
		target = "."
	}
	target = strings.Trim(target, "\"'")
	resolved, err := rt.resolveInteractiveShellPath(target)
	if err != nil {
		return err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", target)
	}
	rt.workspace.Root = resolved
	rt.session.WorkingDir = resolved
	if err := rt.reloadSessionContext(); err != nil {
		return err
	}
	if err := rt.store.Save(rt.session); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("Changed directory to "+resolved))
	return nil
}

func (rt *runtimeState) listDirectory(pathArg string) error {
	target := pathArg
	if strings.TrimSpace(target) == "" {
		target = "."
	}
	target = strings.Trim(target, "\"'")
	resolved, err := rt.resolveInteractiveShellPath(target)
	if err != nil {
		return err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", target)
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return err
	}
	type row struct {
		kind     string
		modified string
		size     string
		name     string
		dir      bool
	}
	rows := make([]row, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		item := row{
			kind:     "file",
			modified: info.ModTime().Format("2006-01-02 15:04"),
			size:     fmt.Sprintf("%d", info.Size()),
			name:     entry.Name(),
			dir:      entry.IsDir(),
		}
		if entry.IsDir() {
			item.kind = "dir"
			item.size = "-"
			item.name += "/"
		}
		rows = append(rows, item)
	}
	slices.SortFunc(rows, func(a, b row) int {
		if a.dir != b.dir {
			if a.dir {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name))
	})

	var lines []string
	lines = append(lines, resolved, "")
	lines = append(lines, fmt.Sprintf("%-5s %-16s %12s %s", "TYPE", "MODIFIED", "SIZE", "NAME"))
	lines = append(lines, fmt.Sprintf("%-5s %-16s %12s %s", "-----", "----------------", "------------", "----"))
	for _, item := range rows {
		lines = append(lines, fmt.Sprintf("%-5s %-16s %12s %s", item.kind, item.modified, item.size, item.name))
	}
	fmt.Fprintln(rt.writer, rt.ui.shell(strings.Join(lines, "\n")))
	return nil
}

func (rt *runtimeState) resolveInteractiveShellPath(pathArg string) (string, error) {
	return resolveInteractiveShellPath(rt.workspace, rt.session, pathArg)
}

func resolveInteractiveShellPath(workspace Workspace, session *Session, pathArg string) (string, error) {
	target := strings.TrimSpace(pathArg)
	if target == "" {
		target = "."
	}
	currentRoot := firstNonBlankString(workspace.Root, workspace.BaseRoot)
	abs, err := workspace.resolveAgainstRoot(currentRoot, target)
	if err != nil {
		return "", err
	}
	boundary := interactiveShellNavigationRoot(workspace, session, currentRoot)
	return ensurePathWithinRoot(target, boundary, abs)
}

func interactiveShellNavigationRoot(workspace Workspace, session *Session, currentRoot string) string {
	candidates := []string{}
	if session != nil && session.Worktree != nil && session.Worktree.Active {
		candidates = append(candidates, session.Worktree.Root)
	}
	candidates = append(candidates, workspace.BaseRoot, currentRoot)
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if pathIsWithinRoot(candidate, currentRoot) {
			return candidate
		}
	}
	return currentRoot
}

func ensurePathWithinRoot(originalPath string, root string, target string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if pathIsWithinRoot(rootAbs, targetAbs) {
		return targetAbs, nil
	}
	return "", fmt.Errorf("path is outside the active workspace root: %s", originalPath)
}

func pathIsWithinRoot(root string, target string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !pathRelEscapesRoot(rel)
}

func pathRelEscapesRoot(rel string) bool {
	cleanRel := filepath.Clean(rel)
	return cleanRel == ".." || strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) || filepath.IsAbs(cleanRel)
}

func (rt *runtimeState) readInput(prompt string) (string, error) {
	var lines []string
	currentPrompt := prompt
	initial := rt.prefillInput
	rt.prefillInput = ""
	if rt.consumeRecentRequestCancel() {
		stabilizeConsoleAfterRequestCancel()
	}
	for {
		var historyNav *inputHistoryNavigator
		if len(lines) == 0 {
			historyNav = newInputHistoryNavigator(rt.inputHistoryEntries(), initial)
		}
		line, usedInteractive, err := rt.readInteractiveLine(currentPrompt, initial, historyNav, false)
		if !usedInteractive {
			fmt.Fprint(rt.writer, currentPrompt)
			if initial != "" {
				fmt.Fprint(rt.writer, initial)
			}
			line, err = rt.reader.ReadString('\n')
			if err != nil {
				if errors.Is(err, io.EOF) && line != "" {
					lines = append(lines, initial+strings.TrimRight(line, "\r\n"))
					return strings.Join(lines, "\n"), nil
				}
				return "", err
			}
			line = initial + strings.TrimRight(line, "\r\n")
		} else if err != nil {
			if errors.Is(err, io.EOF) {
				fmt.Fprintln(rt.writer)
			}
			return "", err
		}
		if shouldIgnoreEmptyPromptSubmit(lines, initial, line) {
			currentPrompt = prompt
			initial = ""
			continue
		}
		if strings.HasSuffix(line, `\`) {
			lines = append(lines, strings.TrimSuffix(line, `\`))
			currentPrompt = rt.ui.continuationPrompt()
			initial = ""
			continue
		}
		lines = append(lines, line)
		return strings.Join(lines, "\n"), nil
	}
}

func shouldIgnoreEmptyPromptSubmit(lines []string, initial string, line string) bool {
	return len(lines) == 0 && initial == "" && line == ""
}

func (rt *runtimeState) withPinnedPrompt(fn func() error) error {
	rt.flushAssistantStream()
	resumeThinking := rt.suspendThinkingIndicator()
	defer resumeThinking()
	rt.outputMu.Lock()
	defer rt.outputMu.Unlock()
	rt.clearFooterLineLocked()
	return fn()
}

func (rt *runtimeState) confirm(question string) (bool, error) {
	if rt.autoApproveConfirmation(question) {
		return true, nil
	}
	var allowed bool
	err := rt.withPinnedPrompt(func() error {
		for {
			label := rt.confirmLabel(question)
			answer, usedInteractive, err := rt.readInteractiveLine(label+" ", "", nil, true)
			if !usedInteractive {
				fmt.Fprint(rt.writer, label+" ")
				if rt.reader == nil {
					return ErrPromptCanceled
				}
				answer, err = rt.reader.ReadString('\n')
				if err != nil {
					return err
				}
				answer = strings.TrimSpace(answer)
			} else if err != nil {
				if errors.Is(err, ErrPromptCanceled) {
					allowed = false
					return nil
				}
				return err
			}
			parsedAllowed, always, handled := parseConfirmationAnswer(answer)
			if !handled {
				continue
			}
			if parsedAllowed && always {
				rt.rememberConfirmationApproval(question)
			}
			allowed = parsedAllowed
			return nil
		}
	})
	if err != nil {
		return false, err
	}
	return allowed, nil
}

func (rt *runtimeState) prepareAnalysisDirectorySelection(root string, cfg ProjectAnalysisConfig) (ProjectAnalysisConfig, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		root = rt.workspace.Root
	}
	candidates, err := findAnalysisDirectoryCandidates(root, cfg)
	if err != nil {
		return cfg, err
	}
	if len(candidates) == 0 {
		return cfg, nil
	}
	fmt.Fprintln(rt.writer, rt.ui.warnLine("Potentially unrelated directories were detected during project structure discovery."))
	for _, candidate := range candidates {
		fmt.Fprintln(rt.writer, rt.ui.statusKV(candidate.Path, analysisDirectoryCandidateReasonLabel(candidate.Reason)))
	}
	fmt.Fprintln(rt.writer)
	if !rt.interactive {
		fmt.Fprintln(rt.writer, rt.ui.hintLine("Interactive confirmation unavailable; including these directories in the analysis."))
		fmt.Fprintln(rt.writer)
		return cfg, nil
	}
	updated := cfg
	for _, candidate := range candidates {
		question := fmt.Sprintf("Include directory %q in project analysis?", candidate.Path)
		include, err := rt.confirm(question)
		if err != nil {
			return cfg, err
		}
		if !include {
			updated.ExcludePaths = append(updated.ExcludePaths, candidate.Path)
			fmt.Fprintln(rt.writer, rt.ui.infoLine("Excluding "+candidate.Path+" from this analysis run."))
		}
	}
	fmt.Fprintln(rt.writer)
	return updated, nil
}

func analysisDirectoryCandidateReasonLabel(reason string) string {
	switch strings.TrimSpace(reason) {
	case "hidden":
		return "hidden directory"
	case "external_like":
		return "external-looking directory"
	default:
		return "review candidate"
	}
}

func (rt *runtimeState) autoApproveConfirmation(question string) bool {
	if isWriteApprovalQuestion(question) {
		return rt.alwaysApproveWrites
	}
	if isDiffPreviewQuestion(question) {
		return rt.alwaysApprovePreview
	}
	if isAutoVerificationQuestion(question) {
		return rt.alwaysApproveVerification
	}
	if isGitApprovalQuestion(question) {
		return rt.perms != nil && rt.perms.IsGitAllowed()
	}
	return false
}

func (rt *runtimeState) confirmLabel(question string) string {
	hint := localizedText(rt.cfg, "[y/N, Esc=cancel]", "[y/N, Esc=취소]")
	if isDiffPreviewQuestion(question) {
		hint = localizedText(rt.cfg, "[y/N/a=auto-accept, Esc=cancel]", "[y/N/a=자동 수락, Esc=취소]")
	} else if isAutoVerificationQuestion(question) {
		hint = localizedText(rt.cfg, "[y/N/a=auto-run, Esc=cancel]", "[y/N/a=자동 실행, Esc=취소]")
	} else if supportsAlwaysApproval(question) {
		hint = localizedText(rt.cfg, "[y/N/a=always, Esc=cancel]", "[y/N/a=항상 승인, Esc=취소]")
	}
	return rt.ui.warnLine(question) + " " + rt.ui.dim(hint)
}

func supportsAlwaysApproval(question string) bool {
	return isWriteApprovalQuestion(question) || isDiffPreviewQuestion(question) || isAutoVerificationQuestion(question) || isGitApprovalQuestion(question)
}

func isWriteApprovalQuestion(question string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(question)), "allow write?")
}

func isDiffPreviewQuestion(question string) bool {
	normalized := strings.TrimSpace(question)
	return strings.EqualFold(normalized, diffPreviewQuestionEnglish) || normalized == diffPreviewQuestionKorean
}

func isAutoVerificationQuestion(question string) bool {
	normalized := strings.TrimSpace(question)
	return strings.EqualFold(normalized, autoVerificationQuestionEnglish) || normalized == autoVerificationQuestionKorean
}

const (
	diffPreviewQuestionEnglish      = "Open diff preview?"
	diffPreviewQuestionKorean       = "변경 미리보기를 열까요?"
	autoVerificationQuestionEnglish = "Run automatic verification now?"
	autoVerificationQuestionKorean  = "자동 검증을 실행할까요?"
	reviewRepairQuestionEnglish     = "Continue repairing?"
	reviewRepairQuestionKorean      = "계속 수정할까요?"
)

func diffPreviewQuestion(cfg Config) string {
	return localizedText(cfg, diffPreviewQuestionEnglish, diffPreviewQuestionKorean)
}

func autoVerificationQuestion(cfg Config) string {
	return localizedText(cfg, autoVerificationQuestionEnglish, autoVerificationQuestionKorean)
}

func reviewRepairContinuationQuestion(cfg Config) string {
	return localizedText(cfg, reviewRepairQuestionEnglish, reviewRepairQuestionKorean)
}

func isGitApprovalQuestion(question string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(question)), "allow git?")
}

func parseConfirmationAnswer(answer string) (bool, bool, bool) {
	normalized := strings.ToLower(strings.TrimSpace(answer))
	switch normalized {
	case "y", "yes":
		return true, false, true
	case "a", "always":
		return true, true, true
	case "", "n", "no":
		return false, false, true
	default:
		return false, false, false
	}
}

func (rt *runtimeState) rememberConfirmationApproval(question string) {
	if isWriteApprovalQuestion(question) {
		rt.alwaysApproveWrites = true
		return
	}
	if isDiffPreviewQuestion(question) {
		rt.alwaysApprovePreview = true
		rt.autoAcceptPreviewOnce = true
		return
	}
	if isAutoVerificationQuestion(question) {
		rt.alwaysApproveVerification = true
		return
	}
	if isGitApprovalQuestion(question) && rt.perms != nil {
		rt.perms.RememberGitApproval()
	}
}

func sessionApprovalStateLabel(enabled bool, mode string) string {
	if enabled {
		return mode + " (session)"
	}
	return "ask"
}

func (rt *runtimeState) promptValue(prompt, defaultValue string) (string, error) {
	return rt.promptValueWithOptions(prompt, defaultValue, false)
}

func (rt *runtimeState) promptValueAllowEmpty(prompt, defaultValue string) (string, error) {
	return rt.promptValueWithOptions(prompt, defaultValue, true)
}

func (rt *runtimeState) promptValueWithOptions(prompt, defaultValue string, allowEmpty bool) (string, error) {
	if !rt.interactive {
		if allowEmpty {
			return "", nil
		}
		if defaultValue != "" {
			return defaultValue, nil
		}
		return "", fmt.Errorf("interactive input unavailable for %s", prompt)
	}
	label := rt.ui.bold(rt.ui.accent(prompt))
	if strings.TrimSpace(defaultValue) != "" {
		label += " " + rt.ui.dim("["+defaultValue+"]")
	}
	label += " " + rt.ui.dim("[Esc to cancel]")
	var line string
	err := rt.withPinnedPrompt(func() error {
		var usedInteractive bool
		var err error
		line, usedInteractive, err = rt.readInteractiveLine(label+" ", "", nil, true)
		if !usedInteractive {
			fmt.Fprint(rt.writer, label+" ")
			line, err = rt.reader.ReadString('\n')
			if err != nil {
				return err
			}
			line = strings.TrimSpace(line)
			return nil
		}
		return err
	})
	if err != nil {
		return "", err
	}
	if line == "" {
		if allowEmpty {
			return "", nil
		}
		return defaultValue, nil
	}
	return line, nil
}

func (rt *runtimeState) promptRequiredValue(prompt, defaultValue string) (string, error) {
	for {
		value, err := rt.promptValue(prompt, defaultValue)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), nil
		}
		fmt.Fprintln(rt.writer, rt.ui.warnLine(prompt+" is required."))
	}
}

func (rt *runtimeState) ensureConfigured() error {
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
		return nil
	}

	if !rt.interactive {
		return fmt.Errorf("provider/model are not configured. Run interactively once or set them in %s, or pass -provider and -model", userConfigPath())
	}

	fmt.Fprintln(rt.writer, rt.ui.section("Initial Setup"))
	fmt.Fprintln(rt.writer, rt.ui.infoLine("Provider and model are not configured yet."))
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Choose a provider and model to continue. This will be saved for future runs."))

	if models, detectedURL, ok := rt.detectLocalOllama(); ok {
		fmt.Fprintln(rt.writer, rt.ui.infoLine("Detected a local Ollama server at "+detectedURL))
		connectNow, err := rt.confirm("Connect to the detected local Ollama server?")
		if err != nil {
			return err
		}
		if connectNow {
			if err := rt.configureDetectedOllama(detectedURL, models); err != nil {
				return err
			}
			return nil
		}
	}

	provider, err := rt.promptProviderChoice()
	if err != nil {
		if errors.Is(err, ErrPromptCanceled) {
			return nil
		}
		return err
	}
	return rt.configureProviderInteractive(provider)
}

func (rt *runtimeState) detectLocalOllama() ([]OllamaModelInfo, string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()

	models, normalized, err := FetchOllamaModels(ctx, normalizeOllamaBaseURL(""), rt.cfg.APIKey)
	if err != nil || len(models) == 0 {
		return nil, "", false
	}
	return models, normalized, true
}

func (rt *runtimeState) configureDetectedOllama(baseURL string, models []OllamaModelInfo) error {
	rt.ollamaModels = models
	fmt.Fprintln(rt.writer, rt.ui.section("Ollama Models"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("server", baseURL))
	fmt.Fprintln(rt.writer, rt.ui.formatOllamaModels(models, rt.session.Model))
	selected, err := rt.chooseOllamaModel(models)
	if err != nil {
		return err
	}
	if err := rt.activateProvider("ollama", selected.Name, baseURL); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Saved provider=%s model=%s", rt.cfg.Provider, rt.cfg.Model)))
	return nil
}

type providerChoiceOption struct {
	Number string
	ID     string
	Label  string
}

func providerChoiceOptions() []providerChoiceOption {
	return []providerChoiceOption{
		{Number: "1", ID: "openai-codex", Label: "openai-codex-subscription"},
		{Number: "2", ID: "codex-cli", Label: "openai-codex-cli"},
		{Number: "3", ID: "openai", Label: "openai-api"},
		{Number: "4", ID: "anthropic-claude-cli", Label: "anthropic-claude-cli"},
		{Number: "5", ID: "anthropic", Label: "anthropic-api"},
		{Number: "6", ID: "deepseek", Label: "DeepSeek"},
		{Number: "7", ID: "openrouter", Label: "openrouter"},
		{Number: "8", ID: "opencode", Label: "OpenCode Zen"},
		{Number: "9", ID: "opencode-go", Label: "OpenCode Go"},
		{Number: "10", ID: "ollama", Label: "ollama"},
		{Number: "11", ID: "lmstudio", Label: "LM Studio"},
		{Number: "12", ID: "vllm", Label: "vLLM"},
		{Number: "13", ID: "llama.cpp", Label: "llama.cpp"},
	}
}

func providerChoiceCompletionTokens() []string {
	return []string{
		"openai-codex-subscription",
		"openai-codex-cli",
		"openai-api",
		"anthropic-claude-cli",
		"anthropic-api",
		"deepseek",
		"openrouter",
		"opencode",
		"opencode-go",
		"ollama",
		"lmstudio",
		"vllm",
		"llama.cpp",
	}
}

func providerUserLabel(provider string) string {
	normalized := normalizeProviderName(provider)
	if label := reviewProviderDisplayLabel(normalized); label != "" {
		return label
	}
	for _, option := range providerChoiceOptions() {
		if option.ID == normalized {
			return option.Label
		}
	}
	return strings.TrimSpace(provider)
}

func (rt *runtimeState) printProviderChoiceOptions() {
	for _, option := range providerChoiceOptions() {
		fmt.Fprintln(rt.writer, rt.ui.info("  "+option.Number+". "+option.Label))
	}
}

func defaultProviderChoice(provider string) string {
	normalized := normalizeProviderName(provider)
	for _, option := range providerChoiceOptions() {
		if option.ID == normalized {
			return option.Number
		}
	}
	return "1"
}

func resolveProviderChoice(choice string) (string, bool) {
	normalized := normalizeProviderName(choice)
	for _, option := range providerChoiceOptions() {
		if strings.EqualFold(strings.TrimSpace(choice), option.Number) || option.ID == normalized {
			return option.ID, true
		}
	}
	return "", false
}

func providerChoiceHelpText() string {
	labels := make([]string, 0, len(providerChoiceOptions())*2)
	for _, option := range providerChoiceOptions() {
		labels = append(labels, option.Number)
	}
	for _, option := range providerChoiceOptions() {
		labels = append(labels, option.Label)
	}
	return "Choose " + strings.Join(labels, ", ") + "."
}

func (rt *runtimeState) configureClaudeCLICommandInteractive() error {
	if rt == nil || !rt.interactive {
		return nil
	}
	commandDefault := strings.TrimSpace(rt.cfg.ClaudeCLIPath)
	if commandDefault == "" {
		commandDefault = claudeCLIDefaultExecutable
	}
	commandPath, err := rt.promptValue("Claude Code CLI command", commandDefault)
	if err != nil {
		return err
	}
	rt.cfg.ClaudeCLIPath = strings.TrimSpace(commandPath)
	return nil
}

func (rt *runtimeState) promptProviderChoice() (string, error) {
	fmt.Fprintln(rt.writer, rt.ui.section("Providers"))
	rt.printProviderChoiceOptions()
	defaultChoice := defaultProviderChoice(rt.cfg.Provider)
	for {
		choice, err := rt.promptValue("Provider number or name", defaultChoice)
		if err != nil {
			return "", err
		}
		if provider, ok := resolveProviderChoice(choice); ok {
			return provider, nil
		}
		fmt.Fprintln(rt.writer, rt.ui.warnLine(providerChoiceHelpText()))
	}
}

func (rt *runtimeState) configureProviderInteractive(provider string) error {
	provider = normalizeProviderName(provider)
	nextProvider := provider
	nextModel := rt.cfg.Model
	nextBaseURL := rt.cfg.BaseURL
	nextAPIKey := rt.providerAPIKey(provider)
	if strings.TrimSpace(nextAPIKey) == "" && strings.EqualFold(rt.cfg.Provider, provider) {
		nextAPIKey = rt.cfg.APIKey
	}

	switch provider {
	case "ollama":
		defaultURL := normalizeOllamaBaseURL("")
		if strings.TrimSpace(nextBaseURL) != "" && strings.Contains(nextBaseURL, "localhost") {
			defaultURL = normalizeOllamaBaseURL(nextBaseURL)
		}
		url, err := rt.promptValue("Ollama URL", defaultURL)
		if err != nil {
			return err
		}
		url = normalizeOllamaBaseURL(url)
		models, normalized, fetchErr := rt.fetchAndShowOllamaModels(url)
		if fetchErr != nil {
			return fmt.Errorf("could not connect to Ollama server: %w", fetchErr)
		}
		if len(models) == 0 {
			return fmt.Errorf("no Ollama models were returned by %s", normalized)
		}
		rt.ollamaModels = models
		selected, err := rt.chooseOllamaModel(models)
		if err != nil {
			return err
		}
		nextModel = selected.Name
		nextBaseURL = normalized
	case "codex-cli":
		commandDefault := strings.TrimSpace(rt.cfg.CodexCLIPath)
		if commandDefault == "" {
			commandDefault = codexCLIDefaultExecutable
		}
		commandPath, err := rt.promptValue("Codex CLI command", commandDefault)
		if err != nil {
			return err
		}
		rt.cfg.CodexCLIPath = strings.TrimSpace(commandPath)
		model, err := rt.chooseCodexCLIModel(nextModel)
		if err != nil {
			return err
		}
		nextModel = model
		nextBaseURL = ""
		nextAPIKey = ""
	case "anthropic-claude-cli":
		if err := rt.configureClaudeCLICommandInteractive(); err != nil {
			return err
		}
		model, err := rt.chooseClaudeCLIModel(nextModel)
		if err != nil {
			return err
		}
		nextModel = model
		nextBaseURL = ""
		nextAPIKey = ""
	case "openai-codex":
		model, err := rt.chooseOpenAICodexModel(nextModel)
		if err != nil {
			return err
		}
		nextModel = model
		if !strings.EqualFold(normalizeProviderName(rt.cfg.Provider), "openai-codex") {
			nextBaseURL = ""
		}
		nextBaseURL = normalizeOpenAICodexBaseURL(nextBaseURL)
		nextAPIKey = ""
	case "lmstudio", "vllm", "llama.cpp":
		localBaseURL := nextBaseURL
		if !strings.EqualFold(normalizeProviderName(rt.cfg.Provider), provider) {
			localBaseURL = ""
		}
		model, normalized, apiKey, err := rt.configureLocalOpenAICompatibleModel(provider, nextModel, localBaseURL, nextAPIKey, "main provider")
		if err != nil {
			return err
		}
		nextModel = model
		nextBaseURL = normalized
		nextAPIKey = apiKey
	case "opencode", "opencode-go":
		opencodeBaseURL := normalizeOpenCodeProviderBaseURL(provider, "")
		if strings.EqualFold(rt.cfg.Provider, provider) && strings.TrimSpace(nextBaseURL) != "" {
			opencodeBaseURL = normalizeOpenCodeProviderBaseURL(provider, nextBaseURL)
		}
		nextBaseURL = opencodeBaseURL
		if strings.TrimSpace(nextAPIKey) == "" {
			keyPrompt := providerDisplayName(provider) + " API key"
			apiKey, err := rt.promptRequiredValue(keyPrompt, "")
			if err != nil {
				return err
			}
			nextAPIKey = apiKey
		}
		models, normalized, err := rt.fetchAndShowOpenCodeModelsForProvider(provider, nextBaseURL, nextAPIKey)
		if err != nil {
			return err
		}
		nextModel, err = rt.chooseOpenCodeModelForProvider(provider, models, nextModel)
		if err != nil {
			return err
		}
		nextBaseURL = normalized
	case "anthropic", "openai", "openrouter", "deepseek":
		baseURL := ""
		if provider == "openrouter" {
			baseURL = normalizeOpenRouterBaseURL("")
		} else if provider == "deepseek" {
			baseURL = normalizeDeepSeekBaseURL("")
			if strings.EqualFold(normalizeProviderName(rt.cfg.Provider), provider) && strings.TrimSpace(nextBaseURL) != "" {
				baseURL = normalizeDeepSeekBaseURL(nextBaseURL)
			}
		}
		nextBaseURL = baseURL
		if strings.TrimSpace(nextAPIKey) == "" {
			keyPrompt := providerDisplayName(provider) + " API key"
			apiKey, err := rt.promptRequiredValue(keyPrompt, "")
			if err != nil {
				return err
			}
			nextAPIKey = apiKey
		}
		switch provider {
		case "anthropic":
			model, err := rt.chooseAnthropicModel(nextModel)
			if err != nil {
				return err
			}
			nextModel = model
		case "openrouter":
			models, normalized, err := rt.fetchAndShowOpenRouterModels(baseURL, nextAPIKey)
			if err != nil {
				return err
			}
			selected, err := rt.chooseOpenRouterModel(models)
			if err != nil {
				return err
			}
			nextModel = selected.ID
			nextBaseURL = normalized
		case "deepseek":
			model, normalized, err := rt.fetchAndChooseDeepSeekModel(baseURL, nextAPIKey, nextModel)
			if err != nil {
				return err
			}
			nextModel = model
			nextBaseURL = normalized
		default:
			model, err := rt.chooseOpenAIModel(nextModel)
			if err != nil {
				return err
			}
			nextModel = model
		}
	default:
		return fmt.Errorf("unsupported provider: %s", provider)
	}

	rt.cfg.APIKey = nextAPIKey
	rt.storeProviderKey(nextProvider, nextAPIKey)
	if err := rt.activateProvider(nextProvider, nextModel, nextBaseURL); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Saved provider=%s model=%s", providerUserLabel(rt.cfg.Provider), rt.cfg.Model)))
	if rt.hasInheritedRoleModels() {
		fmt.Fprintln(rt.writer, rt.ui.info("Only the main model changed. Analysis worker follows main when unset; analysis reviewer stays disabled until explicitly configured. Use /review models for common review roles."))
	}
	return nil
}

func (rt *runtimeState) storedProviderKey(provider string) string {
	if rt == nil || rt.cfg.ProviderKeys == nil {
		return ""
	}
	return rt.cfg.ProviderKeys[normalizeProviderName(provider)]
}

func (rt *runtimeState) providerAPIKey(provider string) string {
	provider = normalizeProviderName(provider)
	if provider == "" {
		return ""
	}
	if provider == "codex-cli" || provider == "openai-codex" || provider == "anthropic-claude-cli" {
		return ""
	}
	if key := strings.TrimSpace(rt.storedProviderKey(provider)); key != "" {
		return key
	}
	if provider == "opencode-go" {
		if key := strings.TrimSpace(rt.storedProviderKey("opencode")); key != "" {
			return key
		}
	}
	if rt != nil && strings.EqualFold(rt.cfg.Provider, provider) && strings.TrimSpace(rt.cfg.APIKey) != "" {
		return strings.TrimSpace(rt.cfg.APIKey)
	}
	return ""
}

func (rt *runtimeState) storeProviderKey(provider, key string) {
	if strings.TrimSpace(key) == "" {
		return
	}
	if rt.cfg.ProviderKeys == nil {
		rt.cfg.ProviderKeys = make(map[string]string)
	}
	rt.cfg.ProviderKeys[normalizeProviderName(provider)] = strings.TrimSpace(key)
}

func isMissingKeyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no api key") || strings.Contains(msg, "api key was found")
}

func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return (strings.Contains(msg, "401") || strings.Contains(msg, "unauthorized") || strings.Contains(msg, "authentication_error") || strings.Contains(msg, "invalid x-api-key") || strings.Contains(msg, "invalid api key"))
}

func (rt *runtimeState) handleAuthError() error {
	provider := strings.ToLower(strings.TrimSpace(rt.session.Provider))
	if normalizeProviderName(provider) == "openai-codex" {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("OpenAI Codex OAuth appears to be invalid. Run /codex-auth login or set "+openAICodexAccessTokenEnv+"."))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.warnLine("API key appears to be invalid. Please enter a new key."))
	keyPrompt := providerDisplayName(provider) + " API key"
	apiKey, err := rt.promptRequiredValue(keyPrompt, "")
	if err != nil {
		if errors.Is(err, ErrPromptCanceled) {
			return nil
		}
		return err
	}
	rt.cfg.APIKey = strings.TrimSpace(apiKey)
	rt.storeProviderKey(provider, rt.cfg.APIKey)
	rt.syncClientFromConfig()
	if err := rt.saveUserConfig(); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("API key updated."))
	return nil
}

func (rt *runtimeState) handleOpenAICodexAuthCommand(args string) error {
	parts := strings.Fields(args)
	action := "status"
	if len(parts) > 0 {
		action = strings.ToLower(strings.TrimSpace(parts[0]))
	}
	switch action {
	case "status", "show":
		return rt.showOpenAICodexAuthStatus()
	case "login":
		return rt.loginOpenAICodexOAuth()
	case "logout", "clear", "reset":
		return rt.logoutOpenAICodexOAuth()
	case "path":
		fmt.Fprintln(rt.writer, rt.ui.section("OpenAI Codex OAuth"))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("auth_file", codexOAuthAuthFilePath()))
		return nil
	default:
		return fmt.Errorf("usage: /codex-auth [status|login|logout|path]")
	}
}

func (rt *runtimeState) showOpenAICodexAuthStatus() error {
	path := codexOAuthAuthFilePath()
	fmt.Fprintln(rt.writer, rt.ui.section("OpenAI Codex OAuth"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("auth_file", path))
	if workspaceGuard := forcedChatGPTWorkspaceIDsDisplay(rt.cfg.ForcedChatGPTWorkspaceID); workspaceGuard != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("forced_chatgpt_workspace_id", workspaceGuard))
	}
	if strings.TrimSpace(os.Getenv(openAICodexAuthFileEnv)) != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("auth_file_source", openAICodexAuthFileEnv))
	} else {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("auth_file_source", "kernforge default"))
	}
	if token := strings.TrimSpace(os.Getenv(openAICodexAccessTokenEnv)); token != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("access_token", "set via "+openAICodexAccessTokenEnv))
		if err := validateCodexOAuthTokenWorkspaceID(token, rt.cfg.ForcedChatGPTWorkspaceID); err != nil {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("status", "workspace mismatch"))
			fmt.Fprintln(rt.writer, rt.ui.statusKV("error", err.Error()))
			return nil
		}
		if expiresAt, ok := jwtExpiresAt(token); ok {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("access_token_expires_at", expiresAt.Format(time.RFC3339)))
		}
		return nil
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("status", "not configured"))
		if cliPath := codexCLIOAuthAuthFilePath(); codexOAuthAuthFileUsableForWorkspaces(cliPath, rt.cfg.ForcedChatGPTWorkspaceID) {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("codex_cli_auth", "available at "+cliPath))
			fmt.Fprintln(rt.writer, rt.ui.hintLine("Selecting openai-codex can import the existing Codex CLI login, or run /codex-auth login to create a new Kernforge-owned OAuth file."))
		} else {
			fmt.Fprintln(rt.writer, rt.ui.hintLine("Run /codex-auth login to create a Kernforge-owned OAuth file."))
		}
		return nil
	}
	_, auth, err := readCodexOAuthAuthFile(path)
	if err != nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("status", "invalid"))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("error", err.Error()))
		return nil
	}
	if err := validateCodexOAuthWorkspace(auth.Tokens, rt.cfg.ForcedChatGPTWorkspaceID); err != nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("status", "workspace mismatch"))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("error", err.Error()))
		return nil
	}
	access := strings.TrimSpace(auth.Tokens.AccessToken)
	refresh := strings.TrimSpace(auth.Tokens.RefreshToken)
	if tokenUsable(access, openAICodexTokenRefreshSkew) {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("status", "ready"))
	} else if refresh != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("status", "access token expired; refresh token available"))
	} else {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("status", "access token expired; refresh token missing"))
	}
	fmt.Fprintln(rt.writer, rt.ui.statusKV("auth_mode", valueOrUnset(auth.AuthMode)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("refresh_token", presentState(refresh != "")))
	if expiresAt, ok := jwtExpiresAt(access); ok {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("access_token_expires_at", expiresAt.Format(time.RFC3339)))
	}
	return nil
}

func (rt *runtimeState) loginOpenAICodexOAuth() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if _, err := runCodexOAuthDeviceLoginWithWorkspaces(ctx, rt.writer, codexOAuthAuthFilePath(), nil, rt.cfg.ForcedChatGPTWorkspaceID); err != nil {
		return err
	}
	return nil
}

func (rt *runtimeState) logoutOpenAICodexOAuth() error {
	path := codexOAuthAuthFilePath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("OpenAI Codex OAuth file removed: "+path))
	if strings.TrimSpace(os.Getenv(openAICodexAccessTokenEnv)) != "" {
		fmt.Fprintln(rt.writer, rt.ui.warnLine(openAICodexAccessTokenEnv+" is still set and will continue to override file auth."))
	}
	return nil
}

func (rt *runtimeState) ensureOpenAICodexAuthInteractive() error {
	if token := strings.TrimSpace(os.Getenv(openAICodexAccessTokenEnv)); token != "" {
		if err := validateCodexOAuthTokenWorkspaceID(token, rt.cfg.ForcedChatGPTWorkspaceID); err != nil {
			return err
		}
		return nil
	}
	if codexOAuthAuthFileUsableForWorkspaces("", rt.cfg.ForcedChatGPTWorkspaceID) {
		return nil
	}
	if !rt.interactive {
		return fmt.Errorf("OpenAI Codex OAuth is not configured; run /codex-auth login")
	}
	if cliPath := codexCLIOAuthAuthFilePath(); codexOAuthAuthFileUsableForWorkspaces(cliPath, rt.cfg.ForcedChatGPTWorkspaceID) {
		fmt.Fprintln(rt.writer, rt.ui.infoLine("Existing Codex CLI OAuth login detected."))
		ok, err := rt.confirm("Use existing Codex CLI OAuth login for Kernforge?")
		if err != nil {
			return err
		}
		if ok {
			if err := importCodexCLIOAuthAuthFileWithWorkspaces(codexOAuthAuthFilePath(), rt.cfg.ForcedChatGPTWorkspaceID); err != nil {
				return err
			}
			fmt.Fprintln(rt.writer, rt.ui.successLine("OpenAI Codex OAuth imported from Codex CLI login."))
			return nil
		}
	}
	fmt.Fprintln(rt.writer, rt.ui.warnLine("OpenAI Codex OAuth is not configured for Kernforge."))
	ok, err := rt.confirm("Run OpenAI Codex OAuth login now?")
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("OpenAI Codex OAuth is not configured; run /codex-auth login")
	}
	return rt.loginOpenAICodexOAuth()
}

func openAICodexAuthStatusSummary() string {
	return openAICodexAuthStatusSummaryForWorkspaces(nil)
}

func openAICodexAuthStatusSummaryForWorkspaces(allowedWorkspaceIDs []string) string {
	if token := strings.TrimSpace(os.Getenv(openAICodexAccessTokenEnv)); token != "" {
		if err := validateCodexOAuthTokenWorkspaceID(token, allowedWorkspaceIDs); err != nil {
			return "workspace mismatch"
		}
		return "access token env override"
	}
	_, auth, err := readCodexOAuthAuthFile(codexOAuthAuthFilePath())
	if err != nil {
		if cliPath := codexCLIOAuthAuthFilePath(); codexOAuthAuthFileUsableForWorkspaces(cliPath, allowedWorkspaceIDs) {
			return "Codex CLI login available"
		}
		return "not configured"
	}
	if err := validateCodexOAuthWorkspace(auth.Tokens, allowedWorkspaceIDs); err != nil {
		return "workspace mismatch"
	}
	if tokenUsable(auth.Tokens.AccessToken, openAICodexTokenRefreshSkew) {
		return "ready"
	}
	if strings.TrimSpace(auth.Tokens.RefreshToken) != "" {
		return "refresh available"
	}
	return "expired"
}

func presentState(ok bool) string {
	if ok {
		return "present"
	}
	return "missing"
}

func providerDisplayName(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "anthropic":
		return "Anthropic"
	case "openai", "openai-compatible":
		return "OpenAI"
	case "anthropic-claude-cli":
		return "Claude Code CLI"
	case "deepseek":
		return "DeepSeek"
	case "openrouter":
		return "OpenRouter"
	case "opencode":
		return "OpenCode Zen"
	case "opencode-go":
		return "OpenCode Go"
	case "ollama":
		return "Ollama"
	case "lmstudio":
		return "LM Studio"
	case "vllm":
		return "vLLM"
	case "llama.cpp":
		return "llama.cpp"
	case "openai-codex":
		return "OpenAI Codex"
	case "codex-cli":
		return "Codex CLI"
	default:
		return provider
	}
}

func (rt *runtimeState) handleProfileCommand(args string) error {
	args = strings.TrimSpace(args)
	if len(rt.cfg.Profiles) == 0 {
		if err := rt.rememberCurrentProfileWhenEmpty(); err != nil {
			return err
		}
	}

	rt.cfg.Profiles = normalizedProfileOrder(rt.cfg.Profiles)
	if len(rt.cfg.Profiles) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No saved profiles yet."))
		fmt.Fprintln(rt.writer, rt.ui.hintLine("Use /model to configure the main model; Kernforge saves the selected model automatically."))
		return nil
	}
	rt.renderProfileList()
	if args == "" {
		if !rt.interactive {
			return nil
		}
		choice, err := rt.promptValueAllowEmpty("Profile action", "")
		if err != nil {
			if errors.Is(err, ErrPromptCanceled) {
				return nil
			}
			return err
		}
		args = strings.TrimSpace(choice)
		if args == "" {
			return nil
		}
	}
	action, index, newName, err := parseProfileCommandArgs(args, len(rt.cfg.Profiles))
	if err != nil {
		return err
	}
	if action == "show" {
		return nil
	}
	return rt.applyProfileAction(action, index, newName)
}

func (rt *runtimeState) rememberCurrentProfileWhenEmpty() error {
	if len(rt.cfg.Profiles) != 0 {
		return nil
	}
	profile, ok := rt.activeProviderProfile("")
	if !ok {
		return nil
	}
	rt.cfg.Profiles = upsertProfile(rt.cfg.Profiles, profile, 5)
	if err := rt.saveUserConfig(); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.infoLine("Saved current provider/model as profile "+profile.Name))
	return nil
}

func (rt *runtimeState) renderProfileList() {
	fmt.Fprintln(rt.writer, rt.ui.section("Profiles"))
	if current, ok := rt.currentProfile(); ok {
		fmt.Fprintln(rt.writer, rt.ui.infoLine("Current profile: "+current.Name))
		meta := []string{current.Provider, current.Model}
		if current.BaseURL != "" {
			meta = append(meta, current.BaseURL)
		}
		if current.Pinned {
			meta = append(meta, "pinned")
		}
		fmt.Fprintln(rt.writer, rt.ui.dim(strings.Join(meta, " | ")))
		rt.renderStoredProfileRoleModels(current, "  ")
		fmt.Fprintln(rt.writer)
	}
	for i, profile := range rt.cfg.Profiles {
		label := fmt.Sprintf("%2d. %s", i+1, profile.Name)
		meta := []string{}
		if profile.Pinned {
			meta = append(meta, "pinned")
		}
		if profile.BaseURL != "" {
			meta = append(meta, profile.BaseURL)
		}
		if rt.session != nil &&
			strings.EqualFold(profile.Provider, rt.session.Provider) &&
			strings.EqualFold(profile.Model, rt.session.Model) &&
			strings.EqualFold(strings.TrimSpace(profile.BaseURL), strings.TrimSpace(rt.session.BaseURL)) {
			label += " " + rt.ui.success("[current]")
		}
		if len(meta) > 0 {
			label += "  " + rt.ui.dim(strings.Join(meta, " | "))
		}
		fmt.Fprintln(rt.writer, label)
		rt.renderStoredProfileRoleModels(profile, "    ")
	}
	fmt.Fprintln(rt.writer)
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Enter a number to activate a profile."))
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Use /profile <number>, /profile r<number>, /profile d<number>, or /profile p<number>."))
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Use /model to configure main, review, analysis, or specialist models."))
}

func (rt *runtimeState) renderStoredProfileRoleModels(profile Profile, indent string) {
	roles := profile.RoleModels
	if roles == nil {
		roles = &ProfileRoleModels{}
	}
	rt.renderStoredProfileRoleLine(indent, "analysis_worker", roles.AnalysisWorker, "not configured; follows profile main model")
	if roles.AnalysisReviewer != nil {
		rt.renderStoredProfileRoleLine(indent, "analysis_reviewer", roles.AnalysisReviewer, "")
	} else if roles.AnalysisWorker != nil {
		fmt.Fprintln(rt.writer, indent+rt.ui.statusKV("analysis_reviewer", "not configured; non-root-cause review skipped"))
	} else {
		rt.renderStoredProfileRoleLine(indent, "analysis_reviewer", nil, "not configured; non-root-cause review skipped")
	}
	if len(roles.Specialists) == 0 {
		return
	}
	for _, specialist := range roles.Specialists {
		if strings.TrimSpace(specialist.Name) == "" {
			continue
		}
		label := "specialist:" + specialist.Name
		value := formatProviderModelEffortLabel(specialist.Provider, specialist.Model, specialist.ReasoningEffort)
		if strings.TrimSpace(specialist.BaseURL) != "" {
			value += " | " + strings.TrimSpace(specialist.BaseURL)
		}
		fmt.Fprintln(rt.writer, indent+rt.ui.statusKV(label, value))
	}
}

func (rt *runtimeState) renderStoredProfileRoleLine(indent string, role string, profile *Profile, fallback string) {
	if profile == nil || strings.TrimSpace(profile.Provider) == "" || strings.TrimSpace(profile.Model) == "" {
		fmt.Fprintln(rt.writer, indent+rt.ui.statusKV(role, fallback))
		return
	}
	value := formatProviderModelEffortLabel(profile.Provider, profile.Model, profile.ReasoningEffort)
	if strings.TrimSpace(profile.BaseURL) != "" {
		value += " | " + strings.TrimSpace(profile.BaseURL)
	}
	fmt.Fprintln(rt.writer, indent+rt.ui.statusKV(role, value))
}

func (rt *runtimeState) applyProfileAction(action string, index int, newName string) error {
	selected := rt.cfg.Profiles[index-1]

	switch action {
	case "activate":
		if strings.TrimSpace(selected.APIKey) != "" {
			rt.cfg.APIKey = selected.APIKey
		} else if key := rt.providerAPIKey(selected.Provider); key != "" {
			rt.cfg.APIKey = key
		} else if requiresAPIKey(selected.Provider) {
			keyPrompt := providerDisplayName(selected.Provider) + " API key"
			apiKey, err := rt.promptRequiredValue(keyPrompt, "")
			if err != nil {
				if errors.Is(err, ErrPromptCanceled) {
					return nil
				}
				return err
			}
			rt.cfg.APIKey = apiKey
		}
		rt.storeProviderKey(selected.Provider, rt.cfg.APIKey)
		roleDefaultEffortProvider := rt.applyProfileRoleModels(selected)

		if err := rt.activateProviderWithRoleModels(selected.Provider, selected.Model, selected.BaseURL, true); err != nil {
			return err
		}
		if roleDefaultEffortProvider != "" {
			rt.printReasoningEffortDefaultNotice(roleDefaultEffortProvider, true)
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Activated profile "+selected.Name))
		return nil
	case "rename":
		newName = strings.TrimSpace(newName)
		if newName == "" {
			var err error
			newName, err = rt.promptRequiredValue("New profile name", selected.Name)
			if err != nil {
				if errors.Is(err, ErrPromptCanceled) {
					return nil
				}
				return err
			}
		}
		rt.cfg.Profiles[index-1].Name = newName
		if err := rt.saveUserConfig(); err != nil {
			return err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Renamed profile to "+newName))
		return nil
	case "delete":
		if configProfileKey(selected) == strings.TrimSpace(rt.cfg.ActiveProfileKey) {
			rt.cfg.ActiveProfileKey = ""
		}
		rt.cfg.Profiles = append(rt.cfg.Profiles[:index-1], rt.cfg.Profiles[index:]...)
		if err := rt.saveUserConfig(); err != nil {
			return err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Deleted profile "+selected.Name))
		return nil
	case "pin":
		rt.cfg.Profiles[index-1].Pinned = true
		rt.cfg.Profiles = normalizedProfileOrder(rt.cfg.Profiles)
		if err := rt.saveUserConfig(); err != nil {
			return err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("pinned profile "+selected.Name))
		return nil
	case "unpin":
		rt.cfg.Profiles[index-1].Pinned = false
		rt.cfg.Profiles = normalizedProfileOrder(rt.cfg.Profiles)
		if err := rt.saveUserConfig(); err != nil {
			return err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("unpinned profile "+selected.Name))
		return nil
	case "toggle-pin":
		newPinned := !rt.cfg.Profiles[index-1].Pinned
		rt.cfg.Profiles[index-1].Pinned = newPinned
		rt.cfg.Profiles = normalizedProfileOrder(rt.cfg.Profiles)
		if err := rt.saveUserConfig(); err != nil {
			return err
		}
		state := "unpinned"
		if newPinned {
			state = "pinned"
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine(state+" profile "+selected.Name))
		return nil
	default:
		return fmt.Errorf("unsupported profile action: %s", action)
	}
}

func (rt *runtimeState) currentProfile() (Profile, bool) {
	if rt == nil || rt.session == nil {
		return Profile{}, false
	}
	for _, profile := range rt.cfg.Profiles {
		if strings.EqualFold(profile.Provider, rt.session.Provider) &&
			strings.EqualFold(profile.Model, rt.session.Model) &&
			strings.EqualFold(strings.TrimSpace(profile.BaseURL), strings.TrimSpace(rt.session.BaseURL)) {
			return profile, true
		}
	}
	return Profile{}, false
}

func (rt *runtimeState) activeProviderProfile(name string) (Profile, bool) {
	provider := strings.TrimSpace(rt.cfg.Provider)
	model := strings.TrimSpace(rt.cfg.Model)
	baseURL := strings.TrimSpace(rt.cfg.BaseURL)
	if rt != nil && rt.session != nil {
		if strings.TrimSpace(rt.session.Provider) != "" {
			provider = strings.TrimSpace(rt.session.Provider)
		}
		if strings.TrimSpace(rt.session.Model) != "" {
			model = strings.TrimSpace(rt.session.Model)
		}
		if strings.TrimSpace(rt.session.BaseURL) != "" {
			baseURL = strings.TrimSpace(rt.session.BaseURL)
		}
	}
	if provider == "" || model == "" {
		return Profile{}, false
	}
	if strings.TrimSpace(name) == "" {
		name = profileName(provider, model)
	}
	return Profile{
		Name:            strings.TrimSpace(name),
		Provider:        provider,
		Model:           model,
		BaseURL:         normalizeProfileBaseURL(provider, baseURL),
		APIKey:          rt.providerAPIKey(provider),
		ReasoningEffort: normalizeReasoningEffort(rt.cfg.ReasoningEffort),
		RoleModels:      rt.currentProfileRoleModels(),
	}, true
}

func (rt *runtimeState) currentProfileRoleModels() *ProfileRoleModels {
	if rt == nil {
		return &ProfileRoleModels{}
	}
	roles := &ProfileRoleModels{}
	if rt.cfg.ProjectAnalysis.WorkerProfile != nil {
		roles.AnalysisWorker = cloneProfile(rt.cfg.ProjectAnalysis.WorkerProfile)
	}
	if rt.cfg.ProjectAnalysis.ReviewerProfile != nil {
		roles.AnalysisReviewer = cloneProfile(rt.cfg.ProjectAnalysis.ReviewerProfile)
	}
	for _, profile := range rt.cfg.Specialists.Profiles {
		if strings.TrimSpace(profile.Name) == "" {
			continue
		}
		if strings.TrimSpace(profile.Provider) == "" || strings.TrimSpace(profile.Model) == "" {
			continue
		}
		roles.Specialists = append(roles.Specialists, SpecialistSubagentProfile{
			Name:            strings.TrimSpace(profile.Name),
			Provider:        strings.TrimSpace(profile.Provider),
			Model:           strings.TrimSpace(profile.Model),
			BaseURL:         normalizeOptionalProfileBaseURL(profile.Provider, profile.BaseURL),
			APIKey:          strings.TrimSpace(profile.APIKey),
			ReasoningEffort: normalizeReasoningEffort(profile.ReasoningEffort),
		})
	}
	return roles
}

func cloneProfile(profile *Profile) *Profile {
	if profile == nil {
		return nil
	}
	cloned := *profile
	cloned.Provider = strings.TrimSpace(cloned.Provider)
	cloned.Model = strings.TrimSpace(cloned.Model)
	cloned.BaseURL = normalizeOptionalProfileBaseURL(cloned.Provider, cloned.BaseURL)
	cloned.ReasoningEffort = normalizeReasoningEffort(cloned.ReasoningEffort)
	if strings.TrimSpace(cloned.Name) == "" && strings.TrimSpace(cloned.Provider) != "" && strings.TrimSpace(cloned.Model) != "" {
		cloned.Name = profileName(cloned.Provider, cloned.Model)
	}
	return &cloned
}

func (rt *runtimeState) applyProfileRoleModels(profile Profile) string {
	if rt == nil {
		return ""
	}
	roles := profile.RoleModels
	if roles == nil {
		roles = &ProfileRoleModels{}
	}
	defaultedEffortTarget := ""
	roleEffort := func(label string, provider string, effort string) string {
		resolved, defaulted := reasoningEffortOrDefaultForProvider(provider, effort)
		if defaulted && defaultedEffortTarget == "" {
			defaultedEffortTarget = label
		}
		return resolved
	}
	profileRoleEffort := func(label string, profile *Profile) string {
		if profile == nil {
			return ""
		}
		return roleEffort(label, profile.Provider, profile.ReasoningEffort)
	}
	specialistRoleEffort := func(profile SpecialistSubagentProfile) string {
		label := "specialist " + strings.TrimSpace(profile.Name)
		if strings.TrimSpace(profile.Name) == "" {
			label = "specialist model"
		}
		return roleEffort(label, profile.Provider, profile.ReasoningEffort)
	}
	defaultProfileEffort := func(profile *Profile, label string) {
		if profile == nil {
			return
		}
		profile.ReasoningEffort = profileRoleEffort(label, profile)
	}
	if roles.AnalysisWorker != nil && strings.TrimSpace(roles.AnalysisWorker.Provider) != "" && strings.TrimSpace(roles.AnalysisWorker.Model) != "" {
		defaultProfileEffort(roles.AnalysisWorker, "analysis worker")
	}
	if roles.AnalysisReviewer != nil && strings.TrimSpace(roles.AnalysisReviewer.Provider) != "" && strings.TrimSpace(roles.AnalysisReviewer.Model) != "" {
		defaultProfileEffort(roles.AnalysisReviewer, "analysis reviewer")
	}
	for i, specialist := range roles.Specialists {
		if strings.TrimSpace(specialist.Provider) == "" || strings.TrimSpace(specialist.Model) == "" {
			continue
		}
		roles.Specialists[i].ReasoningEffort = specialistRoleEffort(specialist)
	}
	rt.cfg.ProjectAnalysis.WorkerProfile = profileRoleModelClone(roles.AnalysisWorker, rt)
	rt.cfg.ProjectAnalysis.ReviewerProfile = profileRoleModelClone(roles.AnalysisReviewer, rt)
	rt.applyProfileSpecialistRoleModels(roles.Specialists)
	return defaultedEffortTarget
}

func profileRoleModelClone(profile *Profile, rt *runtimeState) *Profile {
	if profile == nil || strings.TrimSpace(profile.Provider) == "" || strings.TrimSpace(profile.Model) == "" {
		return nil
	}
	cloned := cloneProfile(profile)
	if strings.TrimSpace(cloned.APIKey) == "" && rt != nil {
		cloned.APIKey = rt.providerAPIKey(cloned.Provider)
	}
	if rt != nil {
		rt.storeProviderKey(cloned.Provider, cloned.APIKey)
	}
	return cloned
}

func (rt *runtimeState) applyProfileSpecialistRoleModels(roleSpecialists []SpecialistSubagentProfile) {
	modelsByName := map[string]SpecialistSubagentProfile{}
	for _, profile := range roleSpecialists {
		name := normalizeSpecialistProfileName(profile.Name)
		if name == "" || strings.TrimSpace(profile.Provider) == "" || strings.TrimSpace(profile.Model) == "" {
			continue
		}
		profile.Name = strings.TrimSpace(profile.Name)
		profile.Provider = strings.TrimSpace(profile.Provider)
		profile.Model = strings.TrimSpace(profile.Model)
		profile.BaseURL = normalizeOptionalProfileBaseURL(profile.Provider, profile.BaseURL)
		profile.ReasoningEffort = normalizeReasoningEffort(profile.ReasoningEffort)
		if strings.TrimSpace(profile.APIKey) == "" {
			profile.APIKey = rt.providerAPIKey(profile.Provider)
		}
		modelsByName[name] = profile
		rt.storeProviderKey(profile.Provider, profile.APIKey)
	}
	next := make([]SpecialistSubagentProfile, 0, len(rt.cfg.Specialists.Profiles)+len(modelsByName))
	seen := map[string]bool{}
	for _, existing := range rt.cfg.Specialists.Profiles {
		name := normalizeSpecialistProfileName(existing.Name)
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
	rt.cfg.Specialists.Profiles = normalizeSpecialistProfiles(next)
}

func requiresAPIKey(provider string) bool {
	switch normalizeProviderName(provider) {
	case "anthropic", "openai", "openai-compatible", "deepseek", "openrouter", "opencode", "opencode-go":
		return true
	default:
		return false
	}
}

func isOpenCodeProvider(provider string) bool {
	switch normalizeProviderName(provider) {
	case "opencode", "opencode-go":
		return true
	default:
		return false
	}
}

func (rt *runtimeState) fetchAndShowOpenRouterModels(baseURL, apiKey string) ([]OpenRouterModelInfo, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	models, normalized, err := FetchOpenRouterModels(ctx, baseURL, apiKey)
	if err != nil {
		return nil, normalized, err
	}
	if len(models) == 0 {
		return nil, normalized, fmt.Errorf("no OpenRouter models were returned by %s", normalized)
	}
	fmt.Fprintln(rt.writer, rt.ui.section("OpenRouter Models"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("server", normalized))
	return models, normalized, nil
}

func (rt *runtimeState) fetchAndShowOpenCodeModels(baseURL, apiKey string) ([]OpenCodeModelInfo, string, error) {
	return rt.fetchAndShowOpenCodeModelsForProvider("opencode", baseURL, apiKey)
}

func (rt *runtimeState) fetchAndShowOpenCodeModelsForProvider(provider, baseURL, apiKey string) ([]OpenCodeModelInfo, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	models, normalized, err := fetchOpenCodeModelsForProvider(ctx, provider, baseURL, apiKey)
	if err != nil {
		return nil, normalized, err
	}
	models = normalizeOpenCodeModelInfos(models)
	if len(models) == 0 {
		return nil, normalized, fmt.Errorf("no %s models were returned by %s", providerDisplayName(provider), normalized)
	}
	fmt.Fprintln(rt.writer, rt.ui.section(providerDisplayName(provider)+" Models"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("server", normalized))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("source", "models returned by the configured "+providerDisplayName(provider)+" API key"))
	return models, normalized, nil
}

func (rt *runtimeState) fetchAndChooseDeepSeekModel(baseURL, apiKey string, currentModel string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	models, normalized, err := FetchOpenAICompatibleModels(ctx, "deepseek", normalizeDeepSeekBaseURL(baseURL), apiKey)
	if err != nil {
		return "", normalized, err
	}
	models = prepareDeepSeekModels(models)
	if len(models) == 0 {
		return "", normalized, fmt.Errorf("DeepSeek models API returned no models from %s", normalized)
	}
	fmt.Fprintln(rt.writer, rt.ui.section("DeepSeek Models"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("server", normalized))
	for i, model := range models {
		label := strings.TrimSpace(model.Name)
		if label == "" {
			label = model.ID
		}
		fmt.Fprintf(rt.writer, "  %d. %s  %s\n", i+1, label, rt.ui.dim(model.ID))
	}
	model, err := rt.chooseOpenAICompatibleModel("deepseek", models, currentModel)
	if err != nil {
		return "", normalized, err
	}
	return model, normalized, nil
}

func (rt *runtimeState) chooseOpenRouterModel(models []OpenRouterModelInfo) (OpenRouterModelInfo, error) {
	if len(models) == 0 {
		return OpenRouterModelInfo{}, fmt.Errorf("no OpenRouter models available")
	}
	models = prepareOpenRouterModels(models)
	if !rt.interactive {
		sorted := sortOpenRouterModels(models, "recommended")
		return sorted[0], nil
	}

	const pageSize = 10
	reasoningOnly := false
	recommendedOnly := false
	sortMode := "recommended"
	keyword := ""
	page := 0
	for {
		sortedModels := sortOpenRouterModels(models, sortMode)
		visible := sortedModels
		if recommendedOnly {
			visible = filterOpenRouterRecommendedModels(visible)
		}
		if reasoningOnly {
			visible = filterOpenRouterReasoningModels(visible)
		}
		if strings.TrimSpace(keyword) != "" {
			visible = filterOpenRouterKeywordModels(visible, keyword)
		}
		if len(visible) == 0 {
			fmt.Fprintln(rt.writer, rt.ui.warnLine("No OpenRouter models matched the current filter."))
			reasoningOnly = false
			recommendedOnly = false
			keyword = ""
			visible = sortedModels
		}
		totalPages := (len(visible) + pageSize - 1) / pageSize
		if totalPages == 0 {
			totalPages = 1
		}
		if page >= totalPages {
			page = totalPages - 1
		}
		fmt.Fprintln(rt.writer, rt.ui.formatOpenRouterModels(visible, rt.session.Model, page, pageSize, reasoningOnly, recommendedOnly, sortMode, keyword))
		choice, err := rt.promptValue("Select model number, n, p, r, m, o, or f", "1")
		if err != nil {
			return OpenRouterModelInfo{}, err
		}
		switch strings.ToLower(strings.TrimSpace(choice)) {
		case "n", "next":
			if page+1 < totalPages {
				page++
			}
			continue
		case "p", "prev", "previous":
			if page > 0 {
				page--
			}
			continue
		case "r", "reasoning":
			reasoningOnly = !reasoningOnly
			page = 0
			continue
		case "m", "recommended":
			recommendedOnly = !recommendedOnly
			page = 0
			continue
		case "o", "order", "sort":
			sortMode = nextOpenRouterSortMode(sortMode)
			page = 0
			continue
		case "f", "filter", "keyword":
			value, err := rt.promptValueAllowEmpty("Keyword filter (blank to clear)", keyword)
			if err != nil {
				return OpenRouterModelInfo{}, err
			}
			keyword = strings.TrimSpace(value)
			page = 0
			continue
		}
		start := page * pageSize
		end := start + pageSize
		if end > len(visible) {
			end = len(visible)
		}
		if idx, err := parsePositiveInt(strings.TrimSpace(choice)); err == nil {
			if idx >= 1 && idx <= (end-start) {
				return visible[start+idx-1], nil
			}
		}
		for _, model := range visible {
			if strings.EqualFold(model.ID, strings.TrimSpace(choice)) {
				return model, nil
			}
		}
		fmt.Fprintln(rt.writer, rt.ui.warnLine("Choose a visible number, a full model id, n, p, r, m, o, or f."))
	}
}

func sortOpenRouterModels(models []OpenRouterModelInfo, mode string) []OpenRouterModelInfo {
	out := append([]OpenRouterModelInfo(nil), models...)
	slices.SortFunc(out, func(a, b OpenRouterModelInfo) int {
		switch mode {
		case "price":
			if result := compareOpenRouterPrice(a, b); result != 0 {
				return result
			}
		case "context":
			if result := compareOpenRouterContext(a, b); result != 0 {
				return result
			}
		default:
			if result := compareOpenRouterRecommended(a, b); result != 0 {
				return result
			}
		}
		if result := compareOpenRouterRecommended(a, b); result != 0 {
			return result
		}
		return strings.Compare(strings.ToLower(a.ID), strings.ToLower(b.ID))
	})
	return out
}

func prepareOpenRouterModels(models []OpenRouterModelInfo) []OpenRouterModelInfo {
	out := append([]OpenRouterModelInfo(nil), models...)
	if !containsOpenRouterModel(out, "openrouter/auto") {
		out = append(out, OpenRouterModelInfo{
			ID:          "openrouter/auto",
			Name:        "Auto Router",
			Description: "Recommended automatic routing across top models.",
		})
	}
	return out
}

func containsOpenRouterModel(models []OpenRouterModelInfo, id string) bool {
	for _, model := range models {
		if strings.EqualFold(model.ID, id) {
			return true
		}
	}
	return false
}

func filterOpenRouterReasoningModels(models []OpenRouterModelInfo) []OpenRouterModelInfo {
	filtered := make([]OpenRouterModelInfo, 0, len(models))
	for _, model := range models {
		if openRouterSupportsReasoning(model) {
			filtered = append(filtered, model)
		}
	}
	return filtered
}

func filterOpenRouterRecommendedModels(models []OpenRouterModelInfo) []OpenRouterModelInfo {
	filtered := make([]OpenRouterModelInfo, 0, len(models))
	for _, model := range models {
		if openRouterCuratedPriority(model.ID) > 0 {
			filtered = append(filtered, model)
		}
	}
	return filtered
}

func filterOpenRouterKeywordModels(models []OpenRouterModelInfo, keyword string) []OpenRouterModelInfo {
	needle := strings.ToLower(strings.TrimSpace(keyword))
	if needle == "" {
		return models
	}
	filtered := make([]OpenRouterModelInfo, 0, len(models))
	for _, model := range models {
		text := strings.ToLower(model.ID + " " + model.Name + " " + model.Description)
		if strings.Contains(text, needle) {
			filtered = append(filtered, model)
		}
	}
	return filtered
}

func compareOpenRouterRecommended(a, b OpenRouterModelInfo) int {
	scoreA := openRouterSortScore(a)
	scoreB := openRouterSortScore(b)
	if scoreA != scoreB {
		if scoreA > scoreB {
			return -1
		}
		return 1
	}
	return 0
}

func compareOpenRouterPrice(a, b OpenRouterModelInfo) int {
	priceA, okA := openRouterTotalPrice(a)
	priceB, okB := openRouterTotalPrice(b)
	switch {
	case okA && okB:
		if priceA < priceB {
			return -1
		}
		if priceA > priceB {
			return 1
		}
	case okA:
		return -1
	case okB:
		return 1
	}
	return 0
}

func compareOpenRouterContext(a, b OpenRouterModelInfo) int {
	if a.ContextLength != b.ContextLength {
		if a.ContextLength > b.ContextLength {
			return -1
		}
		return 1
	}
	return 0
}

func openRouterSortScore(model OpenRouterModelInfo) int {
	score := 0
	score += openRouterCuratedPriority(model.ID)
	if openRouterSupportsReasoning(model) {
		score += 100
	}
	id := strings.ToLower(model.ID)
	name := strings.ToLower(model.Name)
	text := id + " " + name + " " + strings.ToLower(model.Description)
	for _, keyword := range []string{"coder", "code", "programming", "software", "dev"} {
		if strings.Contains(text, keyword) {
			score += 25
			break
		}
	}
	if model.ContextLength >= 128000 {
		score += 5
	}
	return score
}

func nextOpenRouterSortMode(current string) string {
	switch current {
	case "recommended":
		return "price"
	case "price":
		return "context"
	default:
		return "recommended"
	}
}

func openRouterTotalPrice(model OpenRouterModelInfo) (float64, bool) {
	prompt, okPrompt := parseOpenRouterPrice(model.Pricing.Prompt)
	completion, okCompletion := parseOpenRouterPrice(model.Pricing.Completion)
	switch {
	case okPrompt && okCompletion:
		return prompt + completion, true
	case okPrompt:
		return prompt, true
	case okCompletion:
		return completion, true
	default:
		return 0, false
	}
}

func parseOpenRouterPrice(raw string) (float64, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func openRouterCuratedPriority(modelID string) int {
	switch strings.ToLower(strings.TrimSpace(modelID)) {
	case "openrouter/auto":
		return 300
	case "anthropic/claude-sonnet-4", "anthropic/claude-3.7-sonnet":
		return 240
	case "openai/gpt-4.1", "openai/gpt-4.1-mini":
		return 220
	case "google/gemini-2.5-pro", "google/gemini-2.5-flash":
		return 210
	case "qwen/qwen3-coder", "qwen/qwen3-235b-a22b":
		return 200
	case "deepseek/deepseek-r1", "deepseek/deepseek-chat-v3":
		return 190
	default:
		return 0
	}
}

func parseProfileAction(input string, max int) (string, int, error) {
	value := strings.TrimSpace(strings.ToLower(input))
	if value == "" {
		return "", 0, fmt.Errorf("profile selection is required")
	}

	action := "activate"
	if strings.HasPrefix(value, "r") || strings.HasPrefix(value, "d") || strings.HasPrefix(value, "p") {
		switch value[0] {
		case 'r':
			action = "rename"
		case 'd':
			action = "delete"
		case 'p':
			action = "pin"
		}
		value = strings.TrimSpace(value[1:])
	}

	index, err := parsePositiveInt(value)
	if err != nil || index < 1 || index > max {
		return "", 0, fmt.Errorf("invalid profile selection: %s", input)
	}
	return action, index, nil
}

func parseProfileCommandArgs(input string, max int) (string, int, string, error) {
	value := strings.TrimSpace(input)
	if value == "" {
		return "show", 0, "", nil
	}
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return "show", 0, "", nil
	}
	head := strings.ToLower(strings.TrimSpace(fields[0]))
	switch head {
	case "add", "create", "new", "save", "save-current", "remember":
		return "", 0, "", fmt.Errorf("profile commands do not register models directly; use /model to choose the model and Kernforge will save the selection automatically")
	case "list", "show", "status":
		return "show", 0, "", nil
	case "activate", "use", "switch", "select":
		if len(fields) < 2 {
			return "", 0, "", fmt.Errorf("usage: /profile %s <number>", fields[0])
		}
		index, err := parseProfileIndex(fields[1], max, value)
		return "activate", index, "", err
	case "rename", "r":
		if len(fields) < 2 {
			return "", 0, "", fmt.Errorf("usage: /profile rename <number> [name]")
		}
		index, err := parseProfileIndex(fields[1], max, value)
		return "rename", index, strings.TrimSpace(strings.Join(fields[2:], " ")), err
	case "delete", "remove", "d":
		if len(fields) < 2 {
			return "", 0, "", fmt.Errorf("usage: /profile delete <number>")
		}
		index, err := parseProfileIndex(fields[1], max, value)
		return "delete", index, "", err
	case "pin":
		if len(fields) < 2 {
			return "", 0, "", fmt.Errorf("usage: /profile pin <number>")
		}
		index, err := parseProfileIndex(fields[1], max, value)
		return "pin", index, "", err
	case "unpin":
		if len(fields) < 2 {
			return "", 0, "", fmt.Errorf("usage: /profile unpin <number>")
		}
		index, err := parseProfileIndex(fields[1], max, value)
		return "unpin", index, "", err
	}
	action, index, err := parseProfileAction(value, max)
	if action == "pin" {
		action = "toggle-pin"
	}
	return action, index, "", err
}

func parseProfileIndex(input string, max int, original string) (int, error) {
	index, err := parsePositiveInt(strings.TrimSpace(input))
	if err != nil || index < 1 || index > max {
		return 0, fmt.Errorf("invalid profile selection: %s", original)
	}
	return index, nil
}

func normalizedProfileOrder(profiles []Profile) []Profile {
	pinned := make([]Profile, 0, len(profiles))
	others := make([]Profile, 0, len(profiles))
	for _, profile := range profiles {
		if profile.Pinned {
			pinned = append(pinned, profile)
		} else {
			others = append(others, profile)
		}
	}
	return append(pinned, others...)
}

func limitProfiles(profiles []Profile, max int) []Profile {
	if len(profiles) <= max {
		return normalizedProfileOrder(profiles)
	}
	ordered := normalizedProfileOrder(profiles)
	return append([]Profile(nil), ordered[:max]...)
}

func upsertProfile(profiles []Profile, profile Profile, max int) []Profile {
	return upsertProfileWithOptions(profiles, profile, max, true)
}

func upsertProfileWithOptions(profiles []Profile, profile Profile, max int, preserveExistingRoleModels bool) []Profile {
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Provider = strings.TrimSpace(profile.Provider)
	profile.Model = strings.TrimSpace(profile.Model)
	profile.BaseURL = normalizeProfileBaseURL(profile.Provider, profile.BaseURL)
	if profile.Name == "" {
		profile.Name = profileName(profile.Provider, profile.Model)
	}
	pinned := make([]Profile, 0, len(profiles)+1)
	others := make([]Profile, 0, len(profiles)+1)
	for _, existing := range profiles {
		if strings.EqualFold(existing.Provider, profile.Provider) &&
			strings.EqualFold(existing.Model, profile.Model) &&
			strings.EqualFold(strings.TrimSpace(existing.BaseURL), strings.TrimSpace(profile.BaseURL)) {
			if strings.TrimSpace(existing.Name) != "" && strings.EqualFold(profile.Name, profileName(profile.Provider, profile.Model)) {
				profile.Name = existing.Name
			}
			if strings.TrimSpace(profile.APIKey) == "" {
				profile.APIKey = existing.APIKey
			}
			if strings.TrimSpace(profile.ReasoningEffort) == "" {
				profile.ReasoningEffort = existing.ReasoningEffort
			}
			if preserveExistingRoleModels && profile.RoleModels == nil {
				profile.RoleModels = existing.RoleModels
			}
			profile.Pinned = existing.Pinned
			continue
		}
		if existing.Pinned {
			pinned = append(pinned, existing)
		} else {
			others = append(others, existing)
		}
	}
	if profile.Pinned {
		pinned = append([]Profile{profile}, pinned...)
	} else {
		others = append([]Profile{profile}, others...)
	}
	return limitProfiles(append(pinned, others...), max)
}

func (rt *runtimeState) syncClientFromConfig() {
	client, clientErr := NewProviderClient(rt.cfg)
	if rt.agent != nil {
		rt.agent.Config = rt.cfg
		rt.agent.Client = client
		rt.agent.ModelRoutes = rt.modelRoutes
		rt.syncAgentReviewerClientFromConfig()
	}
	rt.workspace.VerificationToolPaths = buildVerificationToolPaths(rt.cfg)
	rt.clientErr = clientErr
}

func (rt *runtimeState) syncAgentReviewerClientFromConfig() {
	if rt == nil || rt.agent == nil {
		return
	}
	rt.agent.AuxReviewerClient = nil
	rt.agent.AuxReviewerModel = ""
	reviewCfg, ok := rt.configuredInteractiveReviewModel()
	if !ok {
		rt.agent.ReviewerClient = nil
		rt.agent.ReviewerModel = ""
		return
	}
	reviewerClient, err := createReviewerClient(&reviewCfg, rt.cfg)
	if err != nil {
		rt.agent.ReviewerClient = nil
		rt.agent.ReviewerModel = ""
		return
	}
	rt.agent.ReviewerClient = reviewerClient
	rt.agent.ReviewerModel = reviewCfg.Model
}

func (rt *runtimeState) configuredInteractiveReviewModel() (ReviewModelConfig, bool) {
	if rt == nil {
		return ReviewModelConfig{}, false
	}
	reviewCfg := configReviewHarness(rt.cfg)
	for _, role := range []string{"primary_reviewer", "design_reviewer", "final_gate_reviewer"} {
		roleCfg, ok := reviewCfg.RoleModels[role]
		if !ok {
			continue
		}
		if strings.TrimSpace(roleCfg.Provider) == "" || strings.TrimSpace(roleCfg.Model) == "" {
			continue
		}
		return roleCfg, true
	}
	return ReviewModelConfig{}, false
}

func (rt *runtimeState) saveUserConfig() error {
	if profile := strings.TrimSpace(rt.configProfile); profile != "" {
		return SaveUserProfileConfig(rt.cfg, profile)
	}
	return SaveUserConfig(rt.cfg)
}

func (rt *runtimeState) saveUserConfigReplacingReviewRoleModels() error {
	if profile := strings.TrimSpace(rt.configProfile); profile != "" {
		return SaveUserProfileConfigReplacingReviewRoleModels(rt.cfg, profile)
	}
	return SaveUserConfigReplacingReviewRoleModels(rt.cfg)
}

func (rt *runtimeState) rememberCurrentProfile() {
	rt.rememberCurrentProfileWithRoleModels(true)
}

func (rt *runtimeState) rememberCurrentMainOnlyProfile() {
	rt.rememberCurrentProfileWithRoleModels(false)
}

func (rt *runtimeState) rememberCurrentProfileWithRoleModels(includeRoleModels bool) {
	if rt == nil || rt.session == nil {
		return
	}
	if strings.TrimSpace(rt.session.Provider) == "" || strings.TrimSpace(rt.session.Model) == "" {
		return
	}
	var roleModels *ProfileRoleModels
	if includeRoleModels {
		roleModels = rt.currentProfileRoleModels()
	}
	profile := Profile{
		Name:            profileName(rt.session.Provider, rt.session.Model),
		Provider:        rt.session.Provider,
		Model:           rt.session.Model,
		BaseURL:         rt.session.BaseURL,
		APIKey:          rt.providerAPIKey(rt.session.Provider),
		ReasoningEffort: normalizeReasoningEffort(rt.cfg.ReasoningEffort),
		RoleModels:      roleModels,
	}

	rt.cfg.Profiles = upsertProfileWithOptions(rt.cfg.Profiles, profile, 5, includeRoleModels)
	rt.cfg.ActiveProfileKey = configProfileKey(profile)
}

func (rt *runtimeState) mainReasoningEffortForActivation(providerName, model, baseURL string) (string, bool) {
	providerName = normalizeProviderName(providerName)
	model = strings.TrimSpace(model)
	baseURL = normalizeProfileBaseURL(providerName, baseURL)
	for _, profile := range rt.cfg.Profiles {
		profile = normalizeConfigProfile(profile)
		if !sameProfileRoute(profile.Provider, profile.Model, profile.BaseURL, providerName, model, baseURL) {
			continue
		}
		if effort := normalizeReasoningEffort(profile.ReasoningEffort); effort != "" {
			return effort, false
		}
	}
	if sameProfileRoute(rt.cfg.Provider, rt.cfg.Model, rt.cfg.BaseURL, providerName, model, baseURL) {
		if effort := normalizeReasoningEffort(rt.cfg.ReasoningEffort); effort != "" {
			return effort, false
		}
	}
	return reasoningEffortOrDefaultForProvider(providerName, "")
}

func (rt *runtimeState) profileReasoningEffortForActivation(current *Profile, providerName, model, baseURL string) (string, bool) {
	providerName = normalizeProviderName(providerName)
	model = strings.TrimSpace(model)
	baseURL = normalizeProfileBaseURL(providerName, baseURL)
	if current != nil && sameProfileRoute(current.Provider, current.Model, current.BaseURL, providerName, model, baseURL) {
		if effort := normalizeReasoningEffort(current.ReasoningEffort); effort != "" {
			return effort, false
		}
	}
	return reasoningEffortOrDefaultForProvider(providerName, "")
}

func (rt *runtimeState) specialistReasoningEffortForActivation(name, providerName, model, baseURL string) (string, bool) {
	providerName = normalizeProviderName(providerName)
	model = strings.TrimSpace(model)
	baseURL = normalizeProfileBaseURL(providerName, baseURL)
	if rt != nil {
		target := normalizeSpecialistProfileName(name)
		for _, profile := range rt.cfg.Specialists.Profiles {
			if normalizeSpecialistProfileName(profile.Name) != target {
				continue
			}
			if sameProfileRoute(profile.Provider, profile.Model, profile.BaseURL, providerName, model, baseURL) {
				if effort := normalizeReasoningEffort(profile.ReasoningEffort); effort != "" {
					return effort, false
				}
			}
		}
	}
	return reasoningEffortOrDefaultForProvider(providerName, "")
}

func sameProfileRoute(leftProvider, leftModel, leftBaseURL, rightProvider, rightModel, rightBaseURL string) bool {
	leftProvider = normalizeProviderName(leftProvider)
	rightProvider = normalizeProviderName(rightProvider)
	if !strings.EqualFold(leftProvider, rightProvider) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(leftModel), strings.TrimSpace(rightModel)) {
		return false
	}
	leftBaseURL = normalizeProfileBaseURL(leftProvider, leftBaseURL)
	rightBaseURL = normalizeProfileBaseURL(rightProvider, rightBaseURL)
	return strings.EqualFold(strings.TrimSpace(leftBaseURL), strings.TrimSpace(rightBaseURL))
}

func (rt *runtimeState) activateProvider(providerName, model, baseURL string) error {
	return rt.activateProviderWithRoleModels(providerName, model, baseURL, false)
}

func (rt *runtimeState) activateProviderWithRoleModels(providerName, model, baseURL string, includeRoleModels bool) error {
	providerName = normalizeProviderName(providerName)
	if isOpenCodeProvider(providerName) {
		resolvedModel, resolvedBaseURL, err := rt.resolveOpenCodeModelForProviderAPIKey(providerName, model, baseURL, rt.providerAPIKey(providerName), "main provider")
		if err != nil {
			return err
		}
		model = resolvedModel
		baseURL = resolvedBaseURL
	}
	nextEffort, defaultedEffort := rt.mainReasoningEffortForActivation(providerName, model, baseURL)
	rt.cfg.Provider = providerName
	rt.cfg.Model = model
	rt.cfg.BaseURL = baseURL
	rt.cfg.ReasoningEffort = nextEffort
	if key := rt.providerAPIKey(providerName); key != "" {
		rt.cfg.APIKey = key
		rt.storeProviderKey(providerName, key)
	}
	rt.session.Provider = providerName
	rt.session.Model = model
	rt.session.BaseURL = baseURL
	rt.syncClientFromConfig()
	if includeRoleModels {
		rt.rememberCurrentProfile()
	} else {
		rt.rememberCurrentMainOnlyProfile()
	}
	if err := rt.store.Save(rt.session); err != nil {
		return err
	}
	if err := rt.saveUserConfig(); err != nil {
		return err
	}
	rt.printReasoningEffortDefaultNotice("main "+providerDisplayName(providerName)+" model", defaultedEffort)
	return rt.clientErr
}

func (rt *runtimeState) resolveOpenCodeModelForAPIKey(model string, baseURL string, apiKey string, scope string) (string, string, error) {
	return rt.resolveOpenCodeModelForProviderAPIKey("opencode", model, baseURL, apiKey, scope)
}

func (rt *runtimeState) resolveOpenCodeModelForProviderAPIKey(provider string, model string, baseURL string, apiKey string, scope string) (string, string, error) {
	provider = normalizeProviderName(provider)
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", "", fmt.Errorf("%s API key is required to load models for %s", providerDisplayName(provider), strings.TrimSpace(scope))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	models, normalized, err := fetchOpenCodeModelsForProvider(ctx, provider, baseURL, apiKey)
	if err != nil {
		return "", normalized, fmt.Errorf("could not load %s models for %s with the configured API key: %w", providerDisplayName(provider), strings.TrimSpace(scope), err)
	}
	models = normalizeOpenCodeModelInfos(models)
	if len(models) == 0 {
		return "", normalized, fmt.Errorf("%s models API returned no models for %s", providerDisplayName(provider), strings.TrimSpace(scope))
	}
	selected := normalizeOpenCodeModelAllowEmptyForProvider(provider, model)
	if selected == "" {
		return preferredOpenCodeModelForProvider(provider, models), normalized, nil
	}
	if !openCodeModelInListForProvider(provider, models, selected) {
		return "", normalized, fmt.Errorf("%s model %s is not available for the configured API key while setting %s; choose one of: %s", providerDisplayName(provider), selected, strings.TrimSpace(scope), strings.Join(limitOpenCodeModelLabelsForProvider(provider, models, 8), ", "))
	}
	return selected, normalized, nil
}

func (rt *runtimeState) handleProviderCommand(args string) error {
	parts := strings.Fields(args)
	if len(parts) > 0 && strings.EqualFold(parts[0], "status") {
		return rt.showProviderStatus()
	}
	if len(parts) > 0 {
		provider, ok := resolveProviderChoice(parts[0])
		if !ok {
			return fmt.Errorf("unknown provider: %s", parts[0])
		}
		err := rt.configureProviderInteractive(provider)
		if errors.Is(err, ErrPromptCanceled) {
			return nil
		}
		return err
	}

	fmt.Fprintln(rt.writer, rt.ui.section("Providers"))
	rt.printProviderChoiceOptions()
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Current provider: "+valueOrUnset(rt.session.Provider)))
	defaultChoice := defaultProviderChoice(rt.session.Provider)
	choice, err := rt.promptValue("Select provider", defaultChoice)
	if err != nil {
		if errors.Is(err, ErrPromptCanceled) {
			return nil
		}
		return err
	}
	if strings.TrimSpace(choice) == "" {
		choice = defaultChoice
	}
	provider, ok := resolveProviderChoice(choice)
	if !ok {
		return fmt.Errorf("unknown provider: %s", choice)
	}
	err = rt.configureProviderInteractive(provider)
	if errors.Is(err, ErrPromptCanceled) {
		return nil
	}
	return err
}

func (rt *runtimeState) handleModelCommand(args string) error {
	if strings.TrimSpace(args) != "" {
		return fmt.Errorf("usage: /model")
	}
	if err := rt.showModelRoutingStatus(); err != nil {
		return err
	}
	if !rt.interactive {
		return nil
	}

	fmt.Fprintln(rt.writer)
	fmt.Fprintln(rt.writer, rt.ui.section("Change Model"))
	fmt.Fprintln(rt.writer, rt.ui.info("  1. main session model"))
	fmt.Fprintln(rt.writer, rt.ui.info("  2. analysis worker"))
	fmt.Fprintln(rt.writer, rt.ui.info("  3. analysis reviewer (project analysis only)"))
	fmt.Fprintln(rt.writer, rt.ui.info("  4. specialist subagent"))
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Common /review role models are configured with /review models."))

	choice, err := rt.promptValue("Select model target", "1")
	if errors.Is(err, ErrPromptCanceled) {
		return nil
	}
	if err != nil {
		return err
	}
	return rt.applyModelHubChoice(choice)
}

func (rt *runtimeState) applyModelHubChoice(choice string) error {
	selection := strings.ToLower(strings.TrimSpace(choice))
	var err error
	switch selection {
	case "", "1", "main", "main session", "main session model":
		err = rt.handleProviderCommand("")
	case "2", "analysis worker", "worker":
		err = rt.configureProjectAnalysisRoleInteractive("worker", "")
	case "3", "analysis reviewer", "reviewer":
		err = rt.configureProjectAnalysisRoleInteractive("reviewer", "")
	case "4", "specialist", "specialist subagent", "subagent":
		err = rt.handleSetSpecialistModelCommand("")
	case "plan", "plan-review", "plan reviewer", "plan-review reviewer":
		return fmt.Errorf("plan-review reviewer routing was removed; use /review models design")
	default:
		return fmt.Errorf("unknown model target: %s", choice)
	}
	if errors.Is(err, ErrPromptCanceled) {
		return nil
	}
	return err
}

func (rt *runtimeState) handleEffortCommand(args string) error {
	args = strings.TrimSpace(args)
	if args == "" || strings.EqualFold(args, "status") || strings.EqualFold(args, "show") {
		return rt.showReasoningEffortStatus()
	}
	parts := strings.Fields(args)
	if len(parts) == 1 {
		if validReasoningEffort(parts[0]) {
			return rt.setMainReasoningEffort(parts[0])
		}
		return fmt.Errorf("usage: /effort [target] [undefined|minimal|low|medium|high|xhigh]")
	}
	if len(parts) == 2 {
		return rt.setReasoningEffortTarget(parts[0], parts[1])
	}
	if len(parts) == 3 && strings.EqualFold(parts[0], "specialist") {
		return rt.setSpecialistReasoningEffort(parts[1], parts[2])
	}
	return fmt.Errorf("usage: /effort [main|analysis-worker|analysis-reviewer|specialist <name>] [undefined|minimal|low|medium|high|xhigh]")
}

func (rt *runtimeState) showReasoningEffortStatus() error {
	fmt.Fprintln(rt.writer, rt.ui.section("Reasoning Effort"))
	mainProvider, mainModel, _, _ := rt.currentProviderStatus()
	fmt.Fprintln(rt.writer, rt.ui.statusKV("main", formatProviderModelEffortLabel(mainProvider, mainModel, rt.cfg.ReasoningEffort)))
	if rt.cfg.ProjectAnalysis.WorkerProfile != nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("analysis_worker", formatProviderModelEffortLabel(rt.cfg.ProjectAnalysis.WorkerProfile.Provider, rt.cfg.ProjectAnalysis.WorkerProfile.Model, rt.cfg.ProjectAnalysis.WorkerProfile.ReasoningEffort)))
	} else {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("analysis_worker", inheritedMainModelLabel(mainProvider, mainModel, rt.cfg.ReasoningEffort)))
	}
	if rt.cfg.ProjectAnalysis.ReviewerProfile != nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("analysis_reviewer", formatProviderModelEffortLabel(rt.cfg.ProjectAnalysis.ReviewerProfile.Provider, rt.cfg.ProjectAnalysis.ReviewerProfile.Model, rt.cfg.ProjectAnalysis.ReviewerProfile.ReasoningEffort)))
	} else if rt.cfg.ProjectAnalysis.WorkerProfile != nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("analysis_reviewer", inheritedWorkerModelLabel(rt.cfg.ProjectAnalysis.WorkerProfile.Provider, rt.cfg.ProjectAnalysis.WorkerProfile.Model, rt.cfg.ProjectAnalysis.WorkerProfile.ReasoningEffort)))
	} else {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("analysis_reviewer", inheritedMainModelLabel(mainProvider, mainModel, rt.cfg.ReasoningEffort)))
	}
	for _, profile := range configuredSpecialistProfiles(rt.cfg) {
		if strings.TrimSpace(profile.Name) == "" {
			continue
		}
		fmt.Fprintln(rt.writer, rt.ui.statusKV("specialist:"+profile.Name, rt.describeSpecialistModel(profile)))
	}
	fmt.Fprintln(rt.writer, rt.ui.statusKV("applies_to", "openai-codex Responses and DeepSeek thinking-mode requests per configured model target"))
	return nil
}

func (rt *runtimeState) mainPromptReasoningEffort() string {
	if rt == nil {
		return ""
	}
	provider := ""
	if rt.session != nil {
		provider = strings.TrimSpace(rt.session.Provider)
	}
	if provider == "" {
		provider = strings.TrimSpace(rt.cfg.Provider)
	}
	if !providerSupportsPromptReasoningEffort(provider) {
		return ""
	}
	return reasoningEffortDisplay(rt.cfg.ReasoningEffort)
}

func providerSupportsPromptReasoningEffort(provider string) bool {
	return providerSupportsReasoningEffort(provider)
}

func providerSupportsReasoningEffort(provider string) bool {
	switch normalizeProviderName(provider) {
	case "openai-codex", "deepseek":
		return true
	default:
		return false
	}
}

func defaultReasoningEffortForProvider(provider string) string {
	if providerSupportsReasoningEffort(provider) {
		return "low"
	}
	return ""
}

func reasoningEffortOrDefaultForProvider(provider string, effort string) (string, bool) {
	normalized := normalizeReasoningEffort(effort)
	if normalized != "" {
		return normalized, false
	}
	defaultEffort := defaultReasoningEffortForProvider(provider)
	if defaultEffort == "" {
		return "", false
	}
	return defaultEffort, true
}

func applyDefaultReasoningEffortForProvider(cfg *Config, provider string) bool {
	if cfg == nil {
		return false
	}
	effort, defaulted := reasoningEffortOrDefaultForProvider(provider, cfg.ReasoningEffort)
	cfg.ReasoningEffort = effort
	return defaulted
}

func (rt *runtimeState) printReasoningEffortDefaultNotice(target string, defaulted bool) {
	if !defaulted || rt == nil || rt.writer == nil {
		return
	}
	fmt.Fprintln(rt.writer, rt.ui.infoLine("reasoning_effort was undefined; defaulted to low for "+target+"."))
}

func validateReasoningEffortTarget(provider string, effort string, target string) (string, error) {
	if !validReasoningEffort(effort) {
		return "", fmt.Errorf("invalid reasoning effort %q; use undefined, minimal, low, medium, high, or xhigh", strings.TrimSpace(effort))
	}
	normalized := normalizeReasoningEffort(effort)
	if normalized != "" && !providerSupportsReasoningEffort(provider) {
		return "", fmt.Errorf("%s does not support reasoning effort: %s", strings.TrimSpace(target), providerDisplayName(provider))
	}
	return normalized, nil
}

func (rt *runtimeState) setMainReasoningEffort(effort string) error {
	normalized, err := validateReasoningEffortTarget(rt.cfg.Provider, effort, "main model")
	if err != nil {
		return err
	}
	rt.cfg.ReasoningEffort = normalized
	rt.syncClientFromConfig()
	rt.rememberCurrentProfile()
	if err := rt.saveUserConfig(); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("main reasoning_effort set to "+reasoningEffortDisplay(rt.cfg.ReasoningEffort)))
	return nil
}

func (rt *runtimeState) setReasoningEffortTarget(target string, effort string) error {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "main", "session", "main-model", "main_model":
		return rt.setMainReasoningEffort(effort)
	case "plan", "plan-review", "plan-reviewer", "reviewer":
		return fmt.Errorf("plan-review reviewer routing was removed; configure review role effort with /review models <role> <provider> <model> [reasoning_effort]")
	case "worker", "analysis-worker", "analysis_worker":
		return rt.setAnalysisReasoningEffort("worker", effort)
	case "analysis-reviewer", "analysis_reviewer":
		return rt.setAnalysisReasoningEffort("reviewer", effort)
	default:
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(target)), "specialist:") {
			name := strings.TrimSpace(target[len("specialist:"):])
			return rt.setSpecialistReasoningEffort(name, effort)
		}
		return fmt.Errorf("unknown reasoning effort target: %s", target)
	}
}

func (rt *runtimeState) setAnalysisReasoningEffort(role string, effort string) error {
	profile := rt.cfg.ProjectAnalysis.WorkerProfile
	if strings.EqualFold(role, "reviewer") {
		profile = rt.cfg.ProjectAnalysis.ReviewerProfile
	}
	if profile == nil {
		return fmt.Errorf("analysis %s has no dedicated model; it inherits another target", role)
	}
	normalized, err := validateReasoningEffortTarget(profile.Provider, effort, "analysis "+role)
	if err != nil {
		return err
	}
	profile.ReasoningEffort = normalized
	rt.rememberCurrentProfile()
	if err := rt.saveUserConfig(); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("analysis "+role+" reasoning_effort set to "+reasoningEffortDisplay(normalized)))
	return nil
}

func (rt *runtimeState) setSpecialistReasoningEffort(name string, effort string) error {
	target := normalizeSpecialistProfileName(name)
	if target == "" {
		return fmt.Errorf("usage: /effort specialist <name> [undefined|minimal|low|medium|high|xhigh]")
	}
	profiles := append([]SpecialistSubagentProfile(nil), rt.cfg.Specialists.Profiles...)
	for i := range profiles {
		if normalizeSpecialistProfileName(profiles[i].Name) != target {
			continue
		}
		if strings.TrimSpace(profiles[i].Provider) == "" || strings.TrimSpace(profiles[i].Model) == "" {
			return fmt.Errorf("specialist %s has no dedicated model; it inherits main", name)
		}
		normalized, err := validateReasoningEffortTarget(profiles[i].Provider, effort, "specialist "+strings.TrimSpace(profiles[i].Name))
		if err != nil {
			return err
		}
		profiles[i].ReasoningEffort = normalized
		rt.cfg.Specialists.Profiles = normalizeSpecialistProfiles(profiles)
		rt.rememberCurrentProfile()
		if err := rt.persistSpecialistOverrides(); err != nil {
			return err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("specialist "+strings.TrimSpace(profiles[i].Name)+" reasoning_effort set to "+reasoningEffortDisplay(normalized)))
		return nil
	}
	return fmt.Errorf("unknown specialist profile: %s", name)
}

func (rt *runtimeState) handleOpenCommand(pathArg string) error {
	if strings.TrimSpace(pathArg) == "" {
		return fmt.Errorf("usage: /open <path>")
	}
	path, err := rt.workspace.Resolve(strings.TrimSpace(pathArg))
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("open expects a file, not a directory: %s", pathArg)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !isText(data) {
		return fmt.Errorf("refusing to open a binary file in the text viewer: %s", pathArg)
	}
	resultPath, err := createViewerResultPath()
	if err != nil {
		return err
	}
	defer os.Remove(resultPath)

	if err := OpenTextViewer(path, data, resultPath); err != nil {
		return err
	}
	if selection, err := readViewerSelection(resultPath); err == nil && selection.HasSelection() {
		selection.FilePath = path
		rt.session.AddSelection(selection)
		_ = rt.store.Save(rt.session)
		relative := relOrAbs(rt.workspace.Root, path)
		rt.prefillInput = formatSelectionPrompt(relative, selection.StartLine, selection.EndLine)
		fmt.Fprintln(rt.writer, rt.ui.successLine("Added selection to prompt: "+strings.TrimSpace(rt.prefillInput)))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("Closed viewer for "+relOrAbs(rt.workspace.Root, path)))
	return nil
}

func (rt *runtimeState) showProviderStatus() error {
	provider, model, base, apiKey := rt.currentProviderStatus()
	fmt.Fprintln(rt.writer, rt.ui.section("Provider"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("provider", valueOrUnset(provider)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("base_url", valueOrUnset(base)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("model", valueOrUnset(model)))
	if strings.EqualFold(provider, "codex-cli") {
		commandPath := strings.TrimSpace(rt.cfg.CodexCLIPath)
		if commandPath == "" {
			commandPath = codexCLIDefaultExecutable
		}
		fmt.Fprintln(rt.writer, rt.ui.statusKV("cli", commandPath))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("auth", "ChatGPT OAuth via Codex CLI"))
	} else if strings.EqualFold(provider, "anthropic-claude-cli") {
		commandPath := strings.TrimSpace(rt.cfg.ClaudeCLIPath)
		if commandPath == "" {
			commandPath = claudeCLIDefaultExecutable
		}
		fmt.Fprintln(rt.writer, rt.ui.statusKV("cli", commandPath))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("auth", "Claude Code CLI login"))
	} else if strings.EqualFold(provider, "openai-codex") {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("auth", "ChatGPT OAuth direct Codex Responses"))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("auth_file", codexOAuthAuthFilePath()))
		if workspaceGuard := forcedChatGPTWorkspaceIDsDisplay(rt.cfg.ForcedChatGPTWorkspaceID); workspaceGuard != "" {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("forced_chatgpt_workspace_id", workspaceGuard))
		}
		fmt.Fprintln(rt.writer, rt.ui.statusKV("auth_status", openAICodexAuthStatusSummaryForWorkspaces(rt.cfg.ForcedChatGPTWorkspaceID)))
	} else {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("api_key", providerAPIKeyState(apiKey)))
	}
	if strings.EqualFold(provider, "ollama") {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("cached_models", fmt.Sprintf("%d", len(rt.ollamaModels))))
	}
	if strings.TrimSpace(provider) != "" {
		rt.printProviderBudgetStatus(provider, base, apiKey)
	}
	return nil
}

func (rt *runtimeState) showModelRoutingStatus() error {
	mainProvider, mainModel, _, _ := rt.currentProviderStatus()
	fmt.Fprintln(rt.writer, rt.ui.section("Model Routing"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("main", formatProviderModelEffortLabel(mainProvider, mainModel, rt.cfg.ReasoningEffort)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("analysis_worker", rt.describeProjectAnalysisRoleModel("worker")))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("analysis_reviewer", rt.describeProjectAnalysisRoleModel("reviewer")))
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Common /review role models are separate; use /review models."))
	profiles := configuredSpecialistProfiles(rt.cfg)
	if len(profiles) == 0 {
		return nil
	}
	fmt.Fprintln(rt.writer)
	fmt.Fprintln(rt.writer, rt.ui.subsection("Specialist Subagents (not /review roles)"))
	columnWidth := specialistStatusColumnWidth(profiles)
	for _, profile := range profiles {
		fmt.Fprintln(rt.writer, rt.ui.statusKVAligned(profile.Name, rt.describeSpecialistModel(profile), columnWidth))
	}
	return nil
}

func specialistStatusColumnWidth(profiles []SpecialistSubagentProfile) int {
	width := 25
	for _, profile := range profiles {
		labelWidth := visibleLen(strings.TrimSpace(profile.Name)) + 1
		if labelWidth > width {
			width = labelWidth
		}
	}
	return width
}

func formatProviderModelLabel(provider string, model string) string {
	return valueOrUnset(providerUserLabel(provider)) + " / " + valueOrUnset(strings.TrimSpace(model))
}

func formatProviderModelEffortLabel(provider string, model string, effort string) string {
	label := formatProviderModelLabel(provider, model)
	if providerSupportsReasoningEffort(provider) {
		label += " / effort=" + reasoningEffortDisplay(effort)
	}
	return label
}

func inheritedMainModelLabel(provider string, model string, effort ...string) string {
	currentEffort := ""
	if len(effort) > 0 {
		currentEffort = effort[0]
	}
	return "not configured; follows main model -> " + formatProviderModelEffortLabel(provider, model, currentEffort)
}

func inheritedWorkerModelLabel(provider string, model string, effort ...string) string {
	currentEffort := ""
	if len(effort) > 0 {
		currentEffort = effort[0]
	}
	return "not configured; follows analysis_worker model -> " + formatProviderModelEffortLabel(provider, model, currentEffort)
}

func (rt *runtimeState) hasInheritedRoleModels() bool {
	if rt == nil {
		return false
	}
	if rt.cfg.ProjectAnalysis.WorkerProfile == nil || rt.cfg.ProjectAnalysis.ReviewerProfile == nil {
		return true
	}
	for _, profile := range configuredSpecialistProfiles(rt.cfg) {
		if strings.TrimSpace(profile.Provider) == "" && strings.TrimSpace(profile.Model) == "" {
			return true
		}
	}
	return false
}

func (rt *runtimeState) describeProjectAnalysisRoleModel(role string) string {
	mainProvider, mainModel, _, _ := rt.currentProviderStatus()
	mainLabel := formatProviderModelEffortLabel(mainProvider, mainModel, rt.cfg.ReasoningEffort)
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "worker":
		if rt != nil && rt.cfg.ProjectAnalysis.WorkerProfile != nil {
			return formatProviderModelEffortLabel(rt.cfg.ProjectAnalysis.WorkerProfile.Provider, rt.cfg.ProjectAnalysis.WorkerProfile.Model, rt.cfg.ProjectAnalysis.WorkerProfile.ReasoningEffort)
		}
		return inheritedMainModelLabel(mainProvider, mainModel, rt.cfg.ReasoningEffort)
	case "reviewer":
		if rt != nil && rt.cfg.ProjectAnalysis.ReviewerProfile != nil {
			return formatProviderModelEffortLabel(rt.cfg.ProjectAnalysis.ReviewerProfile.Provider, rt.cfg.ProjectAnalysis.ReviewerProfile.Model, rt.cfg.ProjectAnalysis.ReviewerProfile.ReasoningEffort)
		}
		return "not configured; non-root-cause review skipped"
	default:
		return mainLabel
	}
}

func (rt *runtimeState) currentProviderStatus() (string, string, string, string) {
	provider := normalizeProviderName(firstNonEmptyTrimmed(rt.session.Provider, rt.cfg.Provider))
	model := firstNonEmptyTrimmed(rt.session.Model, rt.cfg.Model)
	baseURL := normalizeProviderBaseURL(provider, firstNonEmptyTrimmed(rt.session.BaseURL, rt.cfg.BaseURL))
	apiKey := rt.providerAPIKey(provider)
	return provider, model, baseURL, apiKey
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func providerAPIKeyState(apiKey string) string {
	if strings.TrimSpace(apiKey) == "" {
		return "(unset)"
	}
	return "configured"
}

func formatOptionalDecimal(value *float64) string {
	if value == nil {
		return "(unset)"
	}
	return strconv.FormatFloat(*value, 'f', 2, 64)
}

func formatOpenRouterLimitValue(value *float64) string {
	if value == nil {
		return "unlimited"
	}
	return formatOptionalDecimal(value)
}

func openRouterKeyKind(status OpenRouterKeyStatus) string {
	parts := make([]string, 0, 2)
	if status.IsManagementKey {
		parts = append(parts, "management")
	}
	if status.IsProvisioningKey {
		parts = append(parts, "provisioning")
	}
	if len(parts) == 0 {
		parts = append(parts, "standard")
	}
	return strings.Join(parts, ", ")
}

func formatOpenRouterAccountBalance(credits OpenRouterCredits) string {
	if credits.TotalCredits == nil || credits.TotalUsage == nil {
		return "(unset)"
	}
	return strconv.FormatFloat(*credits.TotalCredits-*credits.TotalUsage, 'f', 2, 64)
}

func (rt *runtimeState) printProviderBudgetStatus(provider, baseURL, apiKey string) {
	fmt.Fprintln(rt.writer, rt.ui.subsection("Budget"))

	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "anthropic":
		fmt.Fprintln(rt.writer, rt.ui.statusKV("remaining_balance", "Billing page shows the organization's available credit balance."))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("usage_cost_api", "Usage & Cost Admin API exposes historical usage/cost data, not a standard-key live balance endpoint."))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("admin_access", "Organization admin API key required."))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("usage_docs", anthropicUsageCostDocsURL))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("billing_help", anthropicBillingHelpURL))
	case "openai":
		fmt.Fprintln(rt.writer, rt.ui.statusKV("remaining_balance", "No documented exact prepaid-balance API endpoint is available."))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("usage_cost_api", "Organization usage and costs endpoints are available for reporting."))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("rate_limits", "Per-request remaining requests/tokens arrive via x-ratelimit-* headers."))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("usage_docs", openAIUsageAPIDocsURL))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("billing_help", openAIPrepaidBillingHelpURL))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("dashboard_help", openAIUsageDashboardHelpURL))
	case "openai-compatible":
		fmt.Fprintln(rt.writer, rt.ui.statusKV("remaining_balance", "Depends on the upstream provider; no generic standard endpoint is assumed."))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("usage_cost_api", "Use the upstream provider's documented billing and usage endpoints."))
	case "deepseek":
		rt.printDeepSeekBudgetStatus(baseURL, apiKey)
	case "lmstudio", "vllm", "llama.cpp":
		fmt.Fprintln(rt.writer, rt.ui.statusKV("remaining_balance", "Not applicable for local OpenAI-compatible providers."))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("models_api", openAIAPIURL(baseURL, "/v1/models")))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("endpoint_routing", "Uses OpenAI-compatible chat/completions with optional API key support."))
	case "openrouter":
		rt.printOpenRouterBudgetStatus(baseURL, apiKey)
	case "opencode":
		fmt.Fprintln(rt.writer, rt.ui.statusKV("remaining_balance", "Use the OpenCode Zen billing dashboard for credits and reload state."))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("models_api", openCodeAPIURL(baseURL, "/v1/models")))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("endpoint_routing", "GPT/Codex -> responses, Claude -> messages, other compatible Zen models -> chat/completions."))
	case "opencode-go":
		fmt.Fprintln(rt.writer, rt.ui.statusKV("subscription", "OpenCode Go uses subscription usage limits, not OpenCode Zen pay-as-you-go balance."))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("models_api", openCodeProviderAPIURL("opencode-go", baseURL, "/v1/models")))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("endpoint_routing", "MiniMax M2.5/M2.7 -> messages, other Go models -> chat/completions."))
	case "ollama":
		fmt.Fprintln(rt.writer, rt.ui.statusKV("remaining_balance", "Not applicable for local providers."))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("usage_cost_api", "No remote billing API is expected for local model servers."))
	case "codex-cli":
		fmt.Fprintln(rt.writer, rt.ui.statusKV("subscription", "Managed by the installed Codex CLI ChatGPT account or API configuration."))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("models", "Use codex debug models to see models exposed by the current ChatGPT OAuth account."))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("usage_cost_api", "Use Codex CLI/OpenAI account tooling for usage visibility."))
	case "anthropic-claude-cli":
		fmt.Fprintln(rt.writer, rt.ui.statusKV("subscription", "Managed by the installed Claude Code CLI account or API configuration."))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("models", "Use Claude Code CLI model settings or type a Claude-supported model id directly."))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("usage_cost_api", "Use Anthropic/Claude account tooling for usage visibility."))
	case "openai-codex":
		fmt.Fprintln(rt.writer, rt.ui.statusKV("subscription", "Uses ChatGPT OAuth tokens for the Codex backend; no OpenAI API key is required."))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("models", "Uses the models exposed by the current ChatGPT/Codex OAuth account."))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("usage_cost_api", "Use ChatGPT/Codex account tooling for subscription and usage visibility."))
	default:
		fmt.Fprintln(rt.writer, rt.ui.statusKV("remaining_balance", "Unknown provider; budget visibility depends on the upstream API."))
	}
}

func (rt *runtimeState) printOpenRouterBudgetStatus(baseURL, apiKey string) {
	fmt.Fprintln(rt.writer, rt.ui.statusKV("remaining_budget_api", "GET /key exposes key-level limit_remaining and usage."))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("key_docs", openRouterCurrentKeyDocsURL))

	if strings.TrimSpace(apiKey) == "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("lookup", "Skipped: no API key configured."))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	status, _, err := fetchOpenRouterKeyStatus(ctx, baseURL, apiKey)
	if err != nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("lookup", "Failed."))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("lookup_error", err.Error()))
		return
	}

	fmt.Fprintln(rt.writer, rt.ui.statusKV("lookup", "Live key status loaded."))
	if strings.TrimSpace(status.Label) != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("key_label", status.Label))
	}
	fmt.Fprintln(rt.writer, rt.ui.statusKV("key_type", openRouterKeyKind(status)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("limit", formatOpenRouterLimitValue(status.Limit)))
	if status.LimitRemaining != nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("limit_remaining", formatOptionalDecimal(status.LimitRemaining)))
	}
	if status.Usage != nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("usage", formatOptionalDecimal(status.Usage)))
	}
	if status.UsageDaily != nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("usage_daily", formatOptionalDecimal(status.UsageDaily)))
	}
	if status.UsageMonthly != nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("usage_monthly", formatOptionalDecimal(status.UsageMonthly)))
	}
	if status.BYOKUsage != nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("byok_usage", formatOptionalDecimal(status.BYOKUsage)))
	}
	fmt.Fprintln(rt.writer, rt.ui.statusKV("free_tier", fmt.Sprintf("%t", status.IsFreeTier)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("include_byok_in_limit", fmt.Sprintf("%t", status.IncludeBYOKInLimit)))
	if strings.TrimSpace(status.LimitReset) != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("limit_reset", status.LimitReset))
	}
	if strings.TrimSpace(status.ExpiresAt) != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("expires_at", status.ExpiresAt))
	}

	if !status.IsManagementKey {
		return
	}

	credits, _, err := fetchOpenRouterCredits(ctx, baseURL, apiKey)
	fmt.Fprintln(rt.writer, rt.ui.statusKV("credits_api", openRouterCreditsDocsURL))
	if err != nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("account_lookup_error", err.Error()))
		return
	}

	if credits.TotalCredits != nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("account_credits", formatOptionalDecimal(credits.TotalCredits)))
	}
	if credits.TotalUsage != nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("account_usage", formatOptionalDecimal(credits.TotalUsage)))
	}
	fmt.Fprintln(rt.writer, rt.ui.statusKV("account_balance", formatOpenRouterAccountBalance(credits)))
}

func (rt *runtimeState) printDeepSeekBudgetStatus(baseURL, apiKey string) {
	fmt.Fprintln(rt.writer, rt.ui.statusKV("balance_api", "GET /user/balance exposes user balance and availability."))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("balance_docs", deepSeekBalanceDocsURL))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("rate_limits", "DeepSeek dynamically limits concurrency based on server load and returns HTTP 429 when the limit is reached."))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("rate_limit_docs", deepSeekRateLimitDocsURL))

	if strings.TrimSpace(apiKey) == "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("lookup", "Skipped: no API key configured."))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	balance, _, err := fetchDeepSeekBalance(ctx, baseURL, apiKey)
	if err != nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("lookup", "Failed."))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("lookup_error", err.Error()))
		return
	}

	fmt.Fprintln(rt.writer, rt.ui.statusKV("lookup", "Live balance loaded."))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("available", fmt.Sprintf("%t", balance.IsAvailable)))
	for _, item := range balance.BalanceInfos {
		currency := strings.TrimSpace(item.Currency)
		if currency == "" {
			currency = "balance"
		}
		prefix := strings.ToLower(currency)
		if strings.TrimSpace(item.TotalBalance) != "" {
			fmt.Fprintln(rt.writer, rt.ui.statusKV(prefix+"_total_balance", strings.TrimSpace(item.TotalBalance)))
		}
		if strings.TrimSpace(item.GrantedBalance) != "" {
			fmt.Fprintln(rt.writer, rt.ui.statusKV(prefix+"_granted_balance", strings.TrimSpace(item.GrantedBalance)))
		}
		if strings.TrimSpace(item.ToppedUpBalance) != "" {
			fmt.Fprintln(rt.writer, rt.ui.statusKV(prefix+"_topped_up_balance", strings.TrimSpace(item.ToppedUpBalance)))
		}
	}
}

func (rt *runtimeState) connectOllama(initialURL string) error {
	defaultURL := rt.cfg.BaseURL
	if strings.TrimSpace(defaultURL) == "" || !strings.EqualFold(rt.cfg.Provider, "ollama") {
		defaultURL = normalizeOllamaBaseURL("")
	}
	url := strings.TrimSpace(initialURL)
	if url == "" {
		var err error
		url, err = rt.promptValue("Ollama URL", defaultURL)
		if err != nil {
			return err
		}
	}
	models, normalized, err := rt.fetchAndShowOllamaModels(url)
	if err != nil {
		return err
	}
	rt.ollamaModels = models
	selected, err := rt.chooseOllamaModel(models)
	if err != nil {
		return err
	}
	if err := rt.activateProvider("ollama", selected.Name, normalized); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Connected to %s and selected %s", normalized, selected.Name)))
	return nil
}

func (rt *runtimeState) fetchAndShowOllamaModels(url string) ([]OllamaModelInfo, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	models, normalized, err := FetchOllamaModels(ctx, url, rt.cfg.APIKey)
	if err != nil {
		return nil, normalized, err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Ollama Models"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("server", normalized))
	fmt.Fprintln(rt.writer, rt.ui.formatOllamaModels(models, rt.session.Model))
	return models, normalized, nil
}

var anthropicModels = []struct {
	ID   string
	Name string
}{
	{"claude-sonnet-4-7", "Claude Sonnet 4.7"},
	{"claude-opus-4-7", "Claude Opus 4.7"},
	{"claude-sonnet-4-6", "Claude Sonnet 4.6"},
	{"claude-opus-4-6", "Claude Opus 4.6"},
}

func (rt *runtimeState) chooseAnthropicModel(currentModel string) (string, error) {
	if !rt.interactive {
		if strings.TrimSpace(currentModel) != "" {
			return currentModel, nil
		}
		return anthropicModels[0].ID, nil
	}

	fmt.Fprintln(rt.writer, rt.ui.section("Anthropic Models"))
	defaultChoice := "1"
	for i, m := range anthropicModels {
		marker := ""
		if strings.EqualFold(m.ID, currentModel) {
			marker = " " + rt.ui.success("[current]")
			defaultChoice = fmt.Sprintf("%d", i+1)
		}
		fmt.Fprintf(rt.writer, "  %d. %s  %s%s\n", i+1, m.Name, rt.ui.dim(m.ID), marker)
	}

	choice, err := rt.promptValue("Select model", defaultChoice)
	if err != nil {
		return "", err
	}
	choice = strings.TrimSpace(choice)

	// Accept number
	if idx, err := strconv.Atoi(choice); err == nil {
		if idx >= 1 && idx <= len(anthropicModels) {
			return anthropicModels[idx-1].ID, nil
		}
		return "", fmt.Errorf("invalid selection: %s", choice)
	}
	// Accept model ID directly
	for _, m := range anthropicModels {
		if strings.EqualFold(m.ID, choice) {
			return m.ID, nil
		}
	}
	return "", fmt.Errorf("unknown model: %s", choice)
}

var openAIModels = []struct {
	ID   string
	Name string
}{
	{"gpt-5.4", "GPT 5.4"},
	{"codex-5.3", "Codex 5.3"},
}

func (rt *runtimeState) chooseOpenAIModel(currentModel string) (string, error) {
	if !rt.interactive {
		if strings.TrimSpace(currentModel) != "" {
			return currentModel, nil
		}
		return openAIModels[0].ID, nil
	}

	fmt.Fprintln(rt.writer, rt.ui.section("OpenAI Models"))
	defaultChoice := "1"
	for i, m := range openAIModels {
		marker := ""
		if strings.EqualFold(m.ID, currentModel) {
			marker = " " + rt.ui.success("[current]")
			defaultChoice = fmt.Sprintf("%d", i+1)
		}
		fmt.Fprintf(rt.writer, "  %d. %s  %s%s\n", i+1, m.Name, rt.ui.dim(m.ID), marker)
	}

	choice, err := rt.promptValue("Select model", defaultChoice)
	if err != nil {
		return "", err
	}
	choice = strings.TrimSpace(choice)

	if idx, err := strconv.Atoi(choice); err == nil {
		if idx >= 1 && idx <= len(openAIModels) {
			return openAIModels[idx-1].ID, nil
		}
		return "", fmt.Errorf("invalid selection: %s", choice)
	}
	for _, m := range openAIModels {
		if strings.EqualFold(m.ID, choice) {
			return m.ID, nil
		}
	}
	return "", fmt.Errorf("unknown model: %s", choice)
}

func (rt *runtimeState) configureLocalOpenAICompatibleModel(provider string, currentModel string, baseURL string, apiKey string, scope string) (string, string, string, error) {
	provider = normalizeProviderName(provider)
	defaultURL := normalizeLocalOpenAICompatibleBaseURL(provider, baseURL)
	url := defaultURL
	if rt.interactive {
		var err error
		url, err = rt.promptValue(providerDisplayName(provider)+" URL", defaultURL)
		if err != nil {
			return "", "", "", err
		}
	}
	url = normalizeLocalOpenAICompatibleBaseURL(provider, url)
	models, normalized, err := rt.fetchAndShowOpenAICompatibleModels(provider, url, apiKey)
	if err != nil {
		return "", normalized, "", fmt.Errorf("could not load %s models for %s from %s: %w", providerDisplayName(provider), strings.TrimSpace(scope), normalized, err)
	}
	if len(models) == 0 {
		return "", normalized, "", fmt.Errorf("%s models API returned no models from %s", providerDisplayName(provider), normalized)
	}
	model, err := rt.chooseOpenAICompatibleModel(provider, models, currentModel)
	if err != nil {
		return "", normalized, "", err
	}
	return model, normalized, strings.TrimSpace(apiKey), nil
}

func (rt *runtimeState) fetchAndShowOpenAICompatibleModels(provider string, url string, apiKey string) ([]OpenAICompatibleModelInfo, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	models, normalized, err := FetchOpenAICompatibleModels(ctx, provider, url, apiKey)
	if err != nil {
		return nil, normalized, err
	}
	fmt.Fprintln(rt.writer, rt.ui.section(providerDisplayName(provider)+" Models"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("server", normalized))
	for i, model := range models {
		label := strings.TrimSpace(model.Name)
		if label == "" {
			label = model.ID
		}
		fmt.Fprintf(rt.writer, "  %d. %s  %s\n", i+1, label, rt.ui.dim(model.ID))
	}
	return models, normalized, nil
}

func (rt *runtimeState) chooseOpenAICompatibleModel(provider string, models []OpenAICompatibleModelInfo, currentModel string) (string, error) {
	models = dedupeOpenAICompatibleModels(models)
	if len(models) == 0 {
		return "", fmt.Errorf("no %s models available", providerDisplayName(provider))
	}
	currentModel = strings.TrimSpace(currentModel)
	if !rt.interactive {
		if currentModel != "" {
			for _, model := range models {
				if strings.EqualFold(model.ID, currentModel) {
					return model.ID, nil
				}
			}
		}
		return models[0].ID, nil
	}
	defaultChoice := "1"
	for i, model := range models {
		if strings.EqualFold(model.ID, currentModel) {
			defaultChoice = fmt.Sprintf("%d", i+1)
			break
		}
	}
	choice, err := rt.promptValue("Select model number or id", defaultChoice)
	if err != nil {
		return "", err
	}
	choice = strings.TrimSpace(choice)
	if choice == "" {
		choice = defaultChoice
	}
	if idx, err := strconv.Atoi(choice); err == nil {
		if idx >= 1 && idx <= len(models) {
			return models[idx-1].ID, nil
		}
		return "", fmt.Errorf("invalid selection: %s", choice)
	}
	for _, model := range models {
		if strings.EqualFold(model.ID, choice) || strings.EqualFold(model.Name, choice) {
			return model.ID, nil
		}
	}
	return "", fmt.Errorf("%s model %s was not returned by the configured server; choose one of: %s", providerDisplayName(provider), choice, strings.Join(limitOpenAICompatibleModelLabels(models, 8), ", "))
}

func prepareDeepSeekModels(models []OpenAICompatibleModelInfo) []OpenAICompatibleModelInfo {
	models = dedupeOpenAICompatibleModels(models)
	if len(models) == 0 {
		return models
	}
	rank := map[string]int{
		"deepseek-v4-pro":   0,
		"deepseek-v4-flash": 1,
		"deepseek-reasoner": 2,
		"deepseek-chat":     3,
	}
	sort.SliceStable(models, func(i, j int) bool {
		li := strings.ToLower(strings.TrimSpace(models[i].ID))
		lj := strings.ToLower(strings.TrimSpace(models[j].ID))
		ri, iok := rank[li]
		rj, jok := rank[lj]
		if iok != jok {
			return iok
		}
		if iok && jok && ri != rj {
			return ri < rj
		}
		return li < lj
	})
	return models
}

func limitOpenAICompatibleModelLabels(models []OpenAICompatibleModelInfo, limit int) []string {
	models = dedupeOpenAICompatibleModels(models)
	if limit <= 0 || len(models) <= limit {
		out := make([]string, 0, len(models))
		for _, model := range models {
			out = append(out, model.ID)
		}
		return out
	}
	out := make([]string, 0, limit+1)
	for _, model := range models[:limit] {
		out = append(out, model.ID)
	}
	out = append(out, fmt.Sprintf("...+%d more", len(models)-limit))
	return out
}

type codexCLIModelOption struct {
	ID                          string
	Name                        string
	SupportsImageDetailOriginal bool
}

var codexCLIModels = []codexCLIModelOption{
	{ID: codexCLIDefaultModel, Name: "Codex CLI default", SupportsImageDetailOriginal: true},
	{ID: "gpt-5.5", Name: "GPT-5.5", SupportsImageDetailOriginal: true},
	{ID: "gpt-5.5-pro", Name: "GPT-5.5 Pro", SupportsImageDetailOriginal: true},
	{ID: "gpt-5.4", Name: "GPT-5.4", SupportsImageDetailOriginal: true},
	{ID: "gpt-5.4-pro", Name: "GPT-5.4 Pro", SupportsImageDetailOriginal: true},
	{ID: "gpt-5.4-mini", Name: "GPT-5.4 Mini", SupportsImageDetailOriginal: true},
	{ID: "gpt-5.3-codex", Name: "GPT-5.3 Codex", SupportsImageDetailOriginal: true},
	{ID: "gpt-5.3-codex-spark", Name: "GPT-5.3 Codex Spark", SupportsImageDetailOriginal: true},
	{ID: "gpt-5.2", Name: "GPT-5.2"},
	{ID: "gpt-5.2-codex", Name: "GPT-5.2 Codex"},
	{ID: "gpt-5.1", Name: "GPT-5.1"},
	{ID: "gpt-5.1-codex", Name: "GPT-5.1 Codex"},
	{ID: "gpt-5.1-codex-max", Name: "GPT-5.1 Codex Max"},
	{ID: "gpt-5.1-codex-mini", Name: "GPT-5.1 Codex Mini"},
	{ID: "gpt-5", Name: "GPT-5"},
	{ID: "gpt-5-codex", Name: "GPT-5 Codex"},
	{ID: "codex-mini-latest", Name: "Codex Mini Latest"},
}

func (rt *runtimeState) chooseCodexCLIModel(currentModel string) (string, error) {
	if !rt.interactive {
		if strings.TrimSpace(currentModel) != "" {
			return currentModel, nil
		}
		return codexCLIDefaultModel, nil
	}

	models := rt.codexCLIModelChoices(currentModel)
	fmt.Fprintln(rt.writer, rt.ui.section("Codex CLI Models"))
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Use default to let the installed Codex CLI choose its configured model. Live models are loaded from the ChatGPT OAuth-backed Codex account when available."))
	fmt.Fprintln(rt.writer, rt.ui.hintLine("You can also type any Codex-supported model id directly."))
	defaultChoice := "1"
	for i, m := range models {
		marker := ""
		if strings.EqualFold(m.ID, strings.TrimSpace(currentModel)) {
			marker = " " + rt.ui.success("[current]")
			defaultChoice = fmt.Sprintf("%d", i+1)
		}
		fmt.Fprintf(rt.writer, "  %d. %s  %s%s\n", i+1, m.Name, rt.ui.dim(m.ID), marker)
	}

	choice, err := rt.promptValue("Select model", defaultChoice)
	if err != nil {
		return "", err
	}
	choice = strings.TrimSpace(choice)
	if choice == "" {
		choice = defaultChoice
	}
	if idx, err := strconv.Atoi(choice); err == nil {
		if idx >= 1 && idx <= len(models) {
			return models[idx-1].ID, nil
		}
		return "", fmt.Errorf("invalid selection: %s", choice)
	}
	for _, m := range models {
		if strings.EqualFold(m.ID, choice) {
			return m.ID, nil
		}
	}
	return choice, nil
}

var claudeCLIModels = []codexCLIModelOption{
	{ID: claudeCLIDefaultModel, Name: "Claude Code CLI default"},
	{ID: "sonnet", Name: "Claude Sonnet 4.7 (CLI alias)"},
	{ID: "opus", Name: "Claude Opus 4.7 (CLI alias)"},
	{ID: "haiku", Name: "Claude Haiku (CLI alias)"},
}

func (rt *runtimeState) chooseClaudeCLIModel(currentModel string) (string, error) {
	if !rt.interactive {
		if strings.TrimSpace(currentModel) != "" {
			return currentModel, nil
		}
		return claudeCLIDefaultModel, nil
	}

	models := claudeCLIModelChoices(currentModel)
	fmt.Fprintln(rt.writer, rt.ui.section("Claude Code CLI Models"))
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Use default to let the installed Claude Code CLI choose its configured model. Built-in choices show the current Claude family version but use CLI-safe aliases."))
	fmt.Fprintln(rt.writer, rt.ui.hintLine("You can also type any Claude-supported model id directly."))
	defaultChoice := "1"
	for i, m := range models {
		marker := ""
		if claudeCLIModelChoiceMatchesCurrent(m.ID, currentModel) {
			marker = " " + rt.ui.success("[current]")
			defaultChoice = fmt.Sprintf("%d", i+1)
		}
		fmt.Fprintf(rt.writer, "  %d. %s  %s%s\n", i+1, m.Name, rt.ui.dim(m.ID), marker)
	}

	choice, err := rt.promptValue("Select model", defaultChoice)
	if err != nil {
		return "", err
	}
	choice = strings.TrimSpace(choice)
	if choice == "" {
		choice = defaultChoice
	}
	if idx, err := strconv.Atoi(choice); err == nil {
		if idx >= 1 && idx <= len(models) {
			return models[idx-1].ID, nil
		}
		return "", fmt.Errorf("invalid selection: %s", choice)
	}
	for _, m := range models {
		if strings.EqualFold(m.ID, choice) {
			return m.ID, nil
		}
	}
	return choice, nil
}

func (rt *runtimeState) chooseOpenAICodexModel(currentModel string) (string, error) {
	if !rt.interactive {
		if strings.TrimSpace(currentModel) != "" {
			return currentModel, nil
		}
		return openAICodexDefaultModel, nil
	}
	if err := rt.ensureOpenAICodexAuthInteractive(); err != nil {
		return "", err
	}

	models, remoteCatalogAuthoritative := rt.openAICodexModelChoicesWithSource(currentModel)
	fmt.Fprintln(rt.writer, rt.ui.section("OpenAI Codex Models"))
	if remoteCatalogAuthoritative {
		fmt.Fprintln(rt.writer, rt.ui.hintLine("Live models are loaded from the ChatGPT OAuth-backed Codex account and treated as the source of truth."))
	} else {
		fmt.Fprintln(rt.writer, rt.ui.hintLine("Live models are loaded from the ChatGPT OAuth-backed Codex account when available."))
		fmt.Fprintln(rt.writer, rt.ui.hintLine("You can also type any Codex-supported model id directly."))
	}
	defaultChoice := "1"
	for i, m := range models {
		marker := ""
		if strings.EqualFold(m.ID, strings.TrimSpace(currentModel)) {
			marker = " " + rt.ui.success("[current]")
			defaultChoice = fmt.Sprintf("%d", i+1)
		}
		fmt.Fprintf(rt.writer, "  %d. %s  %s%s\n", i+1, m.Name, rt.ui.dim(m.ID), marker)
	}

	choice, err := rt.promptValue("Select model", defaultChoice)
	if err != nil {
		return "", err
	}
	choice = strings.TrimSpace(choice)
	if choice == "" {
		choice = defaultChoice
	}
	if idx, err := strconv.Atoi(choice); err == nil {
		if idx >= 1 && idx <= len(models) {
			return models[idx-1].ID, nil
		}
		return "", fmt.Errorf("invalid selection: %s", choice)
	}
	for _, m := range models {
		if strings.EqualFold(m.ID, choice) {
			return m.ID, nil
		}
	}
	if remoteCatalogAuthoritative {
		return "", fmt.Errorf("OpenAI Codex model %s was not returned by the ChatGPT account; choose one of: %s", choice, strings.Join(limitCodexCLIModelLabels(models, 8), ", "))
	}
	return choice, nil
}

func claudeCLIModelChoices(currentModel string) []codexCLIModelOption {
	choices := append([]codexCLIModelOption(nil), claudeCLIModels...)
	currentModel = strings.TrimSpace(currentModel)
	if currentModel == "" {
		return choices
	}
	for _, model := range choices {
		if claudeCLIModelChoiceMatchesCurrent(model.ID, currentModel) {
			return choices
		}
	}
	return append(choices, codexCLIModelOption{ID: currentModel, Name: "Current configured model"})
}

func claudeCLIModelChoiceMatchesCurrent(choiceID string, currentModel string) bool {
	choiceID = strings.TrimSpace(choiceID)
	currentModel = strings.TrimSpace(currentModel)
	if strings.EqualFold(choiceID, currentModel) {
		return true
	}
	choiceFlag := claudeCLIModelFlagValue(choiceID)
	currentFlag := claudeCLIModelFlagValue(currentModel)
	return choiceFlag != "" && currentFlag != "" && strings.EqualFold(choiceFlag, currentFlag)
}

func (rt *runtimeState) codexCLIModelChoices(currentModel string) []codexCLIModelOption {
	commandPath := strings.TrimSpace(rt.cfg.CodexCLIPath)
	if commandPath == "" {
		commandPath = codexCLIDefaultExecutable
	}
	workingDir := ""
	if rt.session != nil {
		workingDir = sessionBaseWorkingDir(rt.session)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	liveModels, err := FetchCodexCLIModels(ctx, commandPath, workingDir)
	if err != nil || len(liveModels) == 0 {
		return codexCLIModels
	}
	choices := []codexCLIModelOption{{ID: codexCLIDefaultModel, Name: "Codex CLI default"}}
	seen := map[string]bool{strings.ToLower(codexCLIDefaultModel): true}
	for _, model := range liveModels {
		id := strings.TrimSpace(model.ID)
		if id == "" || seen[strings.ToLower(id)] {
			continue
		}
		name := strings.TrimSpace(model.Name)
		if name == "" {
			name = id
		}
		choices = append(choices, codexCLIModelOption{
			ID:                          id,
			Name:                        name,
			SupportsImageDetailOriginal: model.SupportsImageDetailOriginal,
		})
		seen[strings.ToLower(id)] = true
	}
	currentModel = strings.TrimSpace(currentModel)
	if currentModel != "" && !seen[strings.ToLower(currentModel)] {
		choices = append(choices, codexCLIModelOption{ID: currentModel, Name: "Current configured model"})
	}
	if len(choices) <= 1 {
		return codexCLIModels
	}
	return choices
}

func (rt *runtimeState) openAICodexModelChoices(currentModel string) []codexCLIModelOption {
	choices, _ := rt.openAICodexModelChoicesWithSource(currentModel)
	return choices
}

func (rt *runtimeState) openAICodexModelChoicesWithSource(currentModel string) ([]codexCLIModelOption, bool) {
	baseURL := normalizeOpenAICodexBaseURL("")
	if rt != nil && strings.EqualFold(normalizeProviderName(rt.cfg.Provider), "openai-codex") {
		baseURL = normalizeOpenAICodexBaseURL(rt.cfg.BaseURL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var tokenSource codexOAuthAccessTokenSource
	if rt != nil {
		tokenSource = NewCodexOAuthTokenSourceWithWorkspaceIDs("", nil, rt.cfg.ForcedChatGPTWorkspaceID)
	}
	liveModels, err := FetchOpenAICodexModels(ctx, baseURL, tokenSource, nil)
	choices := make([]codexCLIModelOption, 0, len(liveModels))
	seen := map[string]bool{}
	if err == nil {
		for _, model := range liveModels {
			id := strings.TrimSpace(model.ID)
			if id == "" || seen[strings.ToLower(id)] {
				continue
			}
			name := strings.TrimSpace(model.Name)
			if name == "" {
				name = id
			}
			choices = append(choices, codexCLIModelOption{
				ID:                          id,
				Name:                        name,
				SupportsImageDetailOriginal: model.SupportsImageDetailOriginal,
			})
			seen[strings.ToLower(id)] = true
		}
	}
	remoteCatalogAuthoritative := len(choices) > 0
	if len(choices) == 0 {
		choices = openAICodexFallbackModels()
		for _, choice := range choices {
			seen[strings.ToLower(choice.ID)] = true
		}
	}
	currentModel = strings.TrimSpace(currentModel)
	if currentModel != "" && !remoteCatalogAuthoritative && !seen[strings.ToLower(currentModel)] {
		choices = append(choices, codexCLIModelOption{ID: currentModel, Name: "Current configured model"})
	}
	return choices, remoteCatalogAuthoritative
}

func openAICodexFallbackModels() []codexCLIModelOption {
	out := make([]codexCLIModelOption, 0, len(codexCLIModels))
	for _, model := range codexCLIModels {
		if model.ID == codexCLIDefaultModel {
			continue
		}
		out = append(out, model)
	}
	return out
}

func limitCodexCLIModelLabels(models []codexCLIModelOption, limit int) []string {
	if limit <= 0 || len(models) <= limit {
		out := make([]string, 0, len(models))
		for _, model := range models {
			out = append(out, model.ID)
		}
		return out
	}
	out := make([]string, 0, limit+1)
	for _, model := range models[:limit] {
		out = append(out, model.ID)
	}
	out = append(out, fmt.Sprintf("...+%d more", len(models)-limit))
	return out
}

var openCodeFallbackModels = []OpenCodeModelInfo{
	{ID: "gpt-5.5"},
	{ID: "gpt-5.5-pro"},
	{ID: "gpt-5.4"},
	{ID: "gpt-5.4-pro"},
	{ID: "gpt-5.3-codex"},
	{ID: "gpt-5.3-codex-spark"},
	{ID: "claude-sonnet-4-7"},
	{ID: "claude-opus-4-7"},
	{ID: "glm-5.1"},
	{ID: "glm-5"},
	{ID: "minimax-m2.7"},
	{ID: "minimax-m2.5"},
	{ID: "kimi-k2.6"},
	{ID: "kimi-k2.5"},
	{ID: "qwen3.6-plus"},
	{ID: "qwen3.5-plus"},
}

var openCodeGoFallbackModels = []OpenCodeModelInfo{
	{ID: "deepseek-v4-pro"},
	{ID: "glm-5.1"},
	{ID: "kimi-k2.6"},
	{ID: "qwen3.6-plus"},
	{ID: "minimax-m2.7"},
	{ID: "kimi-k2.5"},
	{ID: "qwen3.5-plus"},
	{ID: "deepseek-v4-flash"},
	{ID: "glm-5"},
	{ID: "minimax-m2.5"},
}

func (rt *runtimeState) chooseOpenCodeModel(models []OpenCodeModelInfo, currentModel string) (string, error) {
	return rt.chooseOpenCodeModelForProvider("opencode", models, currentModel)
}

func (rt *runtimeState) chooseOpenCodeModelForProvider(provider string, models []OpenCodeModelInfo, currentModel string) (string, error) {
	provider = normalizeProviderName(provider)
	models = normalizeOpenCodeModelInfos(models)
	if !rt.interactive {
		current := normalizeOpenCodeModelAllowEmptyForProvider(provider, currentModel)
		if current != "" && (len(models) == 0 || openCodeModelInListForProvider(provider, models, current)) {
			return current, nil
		}
		if len(models) > 0 {
			return preferredOpenCodeModelForProvider(provider, models), nil
		}
		return defaultOpenCodeModelForProvider(provider), nil
	}

	if len(models) == 0 {
		if provider == "opencode-go" {
			models = openCodeGoFallbackModels
		} else {
			models = openCodeFallbackModels
		}
	}

	prefix := openCodeModelPrefix(provider)
	fmt.Fprintln(rt.writer, rt.ui.hintLine(providerDisplayName(provider)+" model ids are saved as "+prefix+"/<model-id>. Bare model ids are normalized automatically."))
	defaultChoice := "1"
	currentModel = normalizeOpenCodeModelAllowEmptyForProvider(provider, currentModel)
	for i, m := range models {
		modelID := normalizeOpenCodeModelForProvider(provider, m.ID)
		marker := ""
		if strings.EqualFold(modelID, currentModel) {
			marker = " " + rt.ui.success("[current]")
			defaultChoice = fmt.Sprintf("%d", i+1)
		}
		fmt.Fprintf(rt.writer, "  %d. %s%s\n", i+1, rt.ui.dim(modelID), marker)
	}
	if currentModel != "" && !openCodeModelInListForProvider(provider, models, currentModel) {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("Current "+providerDisplayName(provider)+" model is not returned by this API key: "+currentModel))
	}

	choice, err := rt.promptValue("Select model", defaultChoice)
	if err != nil {
		return "", err
	}
	choice = strings.TrimSpace(choice)
	if choice == "" {
		choice = defaultChoice
	}
	if idx, err := strconv.Atoi(choice); err == nil {
		if idx >= 1 && idx <= len(models) {
			return normalizeOpenCodeModelForProvider(provider, models[idx-1].ID), nil
		}
		return "", fmt.Errorf("invalid selection: %s", choice)
	}
	for _, m := range models {
		modelID := normalizeOpenCodeModelForProvider(provider, m.ID)
		if strings.EqualFold(modelID, choice) || strings.EqualFold(openCodeAPIModelID(modelID), choice) {
			return modelID, nil
		}
	}
	model := normalizeOpenCodeModelForProvider(provider, choice)
	if len(models) > 0 && !openCodeModelInListForProvider(provider, models, model) {
		return "", fmt.Errorf("%s model %s was not returned by the configured API key; choose one of: %s", providerDisplayName(provider), model, strings.Join(limitOpenCodeModelLabelsForProvider(provider, models, 8), ", "))
	}
	return model, nil
}

func limitOpenCodeModelLabels(models []OpenCodeModelInfo, limit int) []string {
	return limitOpenCodeModelLabelsForProvider("opencode", models, limit)
}

func limitOpenCodeModelLabelsForProvider(provider string, models []OpenCodeModelInfo, limit int) []string {
	models = normalizeOpenCodeModelInfos(models)
	if limit <= 0 || len(models) <= limit {
		out := make([]string, 0, len(models))
		for _, model := range models {
			out = append(out, normalizeOpenCodeModelForProvider(provider, model.ID))
		}
		return out
	}
	out := make([]string, 0, limit+1)
	for _, model := range models[:limit] {
		out = append(out, normalizeOpenCodeModelForProvider(provider, model.ID))
	}
	out = append(out, fmt.Sprintf("...+%d more", len(models)-limit))
	return out
}

func (rt *runtimeState) chooseOllamaModel(models []OllamaModelInfo) (OllamaModelInfo, error) {
	if len(models) == 0 {
		return OllamaModelInfo{}, fmt.Errorf("no Ollama models available on the selected server")
	}
	if !rt.interactive {
		return models[0], nil
	}
	defaultChoice := "1"
	for i, model := range models {
		if model.Name == rt.session.Model {
			defaultChoice = fmt.Sprintf("%d", i+1)
			break
		}
	}
	choice, err := rt.promptValue("Select model number or name", defaultChoice)
	if err != nil {
		return OllamaModelInfo{}, err
	}
	return resolveOllamaModelChoice(choice, models)
}

func (rt *runtimeState) useOllamaModel(choice string) error {
	models := rt.ollamaModels
	if len(models) == 0 {
		refURL := rt.cfg.BaseURL
		if strings.TrimSpace(refURL) == "" {
			refURL = normalizeOllamaBaseURL("")
		}
		var err error
		models, _, err = rt.fetchAndShowOllamaModels(refURL)
		if err != nil {
			return err
		}
		rt.ollamaModels = models
	}
	if strings.TrimSpace(choice) == "" {
		selected, err := rt.chooseOllamaModel(models)
		if err != nil {
			return err
		}
		choice = selected.Name
	}
	selected, err := resolveOllamaModelChoice(choice, models)
	if err != nil {
		return err
	}
	base := rt.cfg.BaseURL
	if strings.TrimSpace(base) == "" {
		base = normalizeOllamaBaseURL("")
	}
	if err := rt.activateProvider("ollama", selected.Name, normalizeOllamaBaseURL(base)); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("Switched to Ollama model "+selected.Name))
	return nil
}

func resolveOllamaModelChoice(choice string, models []OllamaModelInfo) (OllamaModelInfo, error) {
	pick := strings.TrimSpace(choice)
	if pick == "" {
		return OllamaModelInfo{}, fmt.Errorf("model selection is required")
	}
	if idx, err := parsePositiveInt(pick); err == nil {
		if idx >= 1 && idx <= len(models) {
			return models[idx-1], nil
		}
		return OllamaModelInfo{}, fmt.Errorf("model index out of range: %d", idx)
	}
	for _, model := range models {
		if strings.EqualFold(model.Name, pick) || strings.EqualFold(model.Model, pick) {
			return model, nil
		}
	}
	return OllamaModelInfo{}, fmt.Errorf("model not found: %s", pick)
}

func parsePositiveInt(value string) (int, error) {
	n := 0
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + int(ch-'0')
	}
	return n, nil
}

func valueOrUnset(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(unset)"
	}
	return value
}

type uiKV struct {
	Key   string
	Value string
}

func kv(key, value string) uiKV {
	return uiKV{
		Key:   key,
		Value: value,
	}
}

func (rt *runtimeState) printKVGroup(title string, items ...uiKV) {
	if strings.TrimSpace(title) != "" {
		fmt.Fprintln(rt.writer, rt.ui.subsection(title))
	}
	for _, item := range items {
		fmt.Fprintln(rt.writer, rt.ui.statusKV(item.Key, item.Value))
	}
}

var webResearchEnvKeys = []string{
	"TAVILY_API_KEY",
	"BRAVE_SEARCH_API_KEY",
	"SERPAPI_API_KEY",
}

func webResearchConfigKVs(cfg Config, getenv func(string) string) []uiKV {
	if getenv == nil {
		getenv = os.Getenv
	}
	servers := webResearchMCPServers(cfg.MCPServers)
	hasRelevantEnv := false
	for _, key := range webResearchEnvKeys {
		if strings.TrimSpace(getenv(key)) != "" {
			hasRelevantEnv = true
			break
		}
	}
	if len(servers) == 0 && !hasRelevantEnv {
		return nil
	}

	items := []uiKV{
		kv("configured", fmt.Sprintf("%t", len(servers) > 0)),
	}
	if len(servers) > 0 {
		items = append(items,
			kv("servers", strings.Join(webResearchMCPServerLabels(servers), ", ")),
			kv("active_search_key", activeWebResearchKeyLabel(servers, getenv)),
		)
	}
	for _, key := range webResearchEnvKeys {
		items = append(items, kv(strings.ToLower(key), webResearchKeySource(servers, key, getenv)))
	}
	return items
}

func webResearchMCPServers(servers []MCPServerConfig) []MCPServerConfig {
	out := make([]MCPServerConfig, 0, len(servers))
	for _, server := range servers {
		if isWebResearchMCPServer(server) {
			out = append(out, server)
		}
	}
	return out
}

func webResearchMCPServerLabels(servers []MCPServerConfig) []string {
	labels := make([]string, 0, len(servers))
	for _, server := range servers {
		label := strings.TrimSpace(server.Name)
		if label == "" {
			label = deriveMCPServerName(server)
		}
		if label == "" {
			label = "unnamed"
		}
		labels = append(labels, label)
	}
	return labels
}

func activeWebResearchKeyLabel(servers []MCPServerConfig, getenv func(string) string) string {
	for _, key := range webResearchEnvKeys {
		source := webResearchKeySource(servers, key, getenv)
		if source == "unset" {
			continue
		}
		return fmt.Sprintf("%s (%s)", key, source)
	}
	return "unset"
}

func webResearchKeySource(servers []MCPServerConfig, key string, getenv func(string) string) string {
	for _, server := range servers {
		if strings.TrimSpace(server.Env[key]) != "" {
			return "config"
		}
	}
	if strings.TrimSpace(getenv(key)) != "" {
		return "environment"
	}
	return "unset"
}

func webResearchMCPStatusSummary(server MCPServerConfig, getenv func(string) string) string {
	if !isWebResearchMCPServer(server) {
		return ""
	}
	return fmt.Sprintf(
		"  active_key=%s  tavily=%s  brave=%s  serpapi=%s",
		activeWebResearchKeyLabel([]MCPServerConfig{server}, getenv),
		webResearchKeySource([]MCPServerConfig{server}, "TAVILY_API_KEY", getenv),
		webResearchKeySource([]MCPServerConfig{server}, "BRAVE_SEARCH_API_KEY", getenv),
		webResearchKeySource([]MCPServerConfig{server}, "SERPAPI_API_KEY", getenv),
	)
}

func (rt *runtimeState) handleCommand(cmd Command) (bool, error) {
	cmd.Name = normalizeSlashCommandName(cmd.Name)
	switch cmd.Name {
	case "help":
		fmt.Fprintln(rt.writer, rt.ui.section("Help"))
		if detail, ok := HelpDetail(cmd.Args); ok {
			fmt.Fprintln(rt.writer, rt.ui.highlightCommands(detail))
		} else {
			fmt.Fprintln(rt.writer, rt.ui.highlightCommands(HelpText()))
			if strings.TrimSpace(cmd.Args) != "" {
				fmt.Fprintln(rt.writer)
				fmt.Fprintln(rt.writer, rt.ui.warnLine("No detailed help found for "+cmd.Args))
			}
		}
	case "status":
		fmt.Fprintln(rt.writer, rt.ui.section("Status"))
		fmt.Fprintln(rt.writer, rt.ui.hintLine("Current session and runtime state. Use /config for effective settings."))
		fmt.Fprintln(rt.writer)
		rt.printKVGroup("Connection",
			kv("version", currentVersion()),
			kv("provider", valueOrUnset(rt.session.Provider)),
			kv("model", valueOrUnset(rt.session.Model)),
			kv("base_url", valueOrUnset(rt.cfg.BaseURL)),
			kv("permission_mode", valueOrUnset(rt.activePermissionModeSnapshot())),
			kv("active_permission_profile", valueOrUnset(activePermissionProfileIDForModeString(rt.activePermissionModeSnapshot()))),
			kv("progress_display", configProgressDisplay(rt.cfg)),
		)
		fmt.Fprintln(rt.writer)
		rt.printKVGroup("Workspace",
			kv("cwd", rt.session.WorkingDir),
			kv("base_root", sessionBaseWorkingDir(rt.session)),
			kv("workspace_roots", strings.Join(workspaceEffectiveRoots(rt.workspace, rt.session), ", ")),
			kv("session", rt.session.ID),
			kv("sessions_dir", rt.store.Root()),
			kv("runtime_error_log", runtimeErrorLogPath(sessionBaseWorkingDir(rt.session))),
		)
		if rt.session.Worktree != nil {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("worktree_root", valueOrUnset(rt.session.Worktree.Root)))
			fmt.Fprintln(rt.writer, rt.ui.statusKV("worktree_branch", valueOrUnset(rt.session.Worktree.Branch)))
			fmt.Fprintln(rt.writer, rt.ui.statusKV("worktree_active", fmt.Sprintf("%t", rt.session.Worktree.Active)))
		}
		if len(rt.session.SpecialistWorktrees) > 0 {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("specialist_worktrees", fmt.Sprintf("%d", len(rt.session.SpecialistWorktrees))))
		}
		if strings.TrimSpace(rt.session.ActiveFeatureID) != "" {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("active_feature", rt.session.ActiveFeatureID))
		}
		if goal, ok := rt.session.ActiveGoal(); ok {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("active_goal", goal.ID+" ["+goal.Status+"]"))
			fmt.Fprintln(rt.writer, rt.ui.statusKV("active_goal_iteration", fmt.Sprintf("%d", goal.Iteration)))
		}
		if rt.session.ActiveEditLoop != nil {
			loop := *rt.session.ActiveEditLoop
			loop.Normalize()
			fmt.Fprintln(rt.writer)
			fmt.Fprintln(rt.writer, rt.ui.subsection("Edit Loop"))
			fmt.Fprintln(rt.writer, rt.ui.statusKV("edit_loop", firstNonBlankString(loop.ID, "active")))
			fmt.Fprintln(rt.writer, rt.ui.statusKV("edit_loop_status", valueOrUnset(loop.Status)))
			fmt.Fprintln(rt.writer, rt.ui.statusKV("changed_paths", fmt.Sprintf("%d", len(loop.ChangedPaths))))
			if len(loop.ChangedPaths) > 0 {
				fmt.Fprintln(rt.writer, rt.ui.statusKV("latest_changed", strings.Join(limitStrings(loop.ChangedPaths, 4), ", ")))
			}
			fmt.Fprintln(rt.writer, rt.ui.statusKV("worker_evidence", fmt.Sprintf("%d", len(loop.WorkerEvidence))))
			fmt.Fprintln(rt.writer, rt.ui.statusKV("verification", valueOrUnset(firstNonBlankString(loop.VerificationStatus, loop.VerificationSummary))))
			fmt.Fprintln(rt.writer, rt.ui.statusKV("verification_evidence", fmt.Sprintf("%d", len(loop.VerificationEvidence))))
			if loop.VerificationBundleID != "" {
				fmt.Fprintln(rt.writer, rt.ui.statusKV("verification_bundle", loop.VerificationBundleID))
			}
			fmt.Fprintln(rt.writer, rt.ui.statusKV("retry_count", fmt.Sprintf("%d", loop.RetryCount)))
			if len(loop.RetryDecisions) > 0 {
				decision := loop.RetryDecisions[len(loop.RetryDecisions)-1]
				fmt.Fprintln(rt.writer, rt.ui.statusKV("retry_decision", valueOrUnset(decision.Action)))
			}
			fmt.Fprintln(rt.writer, rt.ui.statusKV("remaining_risks", fmt.Sprintf("%d", len(loop.RemainingRisks))))
			if loop.FinalReviewVerdict != "" {
				fmt.Fprintln(rt.writer, rt.ui.statusKV("final_review", loop.FinalReviewVerdict))
			}
		} else if len(rt.session.EditLoops) > 0 {
			loop := rt.session.EditLoops[0]
			loop.Normalize()
			fmt.Fprintln(rt.writer)
			fmt.Fprintln(rt.writer, rt.ui.subsection("Edit Loop"))
			fmt.Fprintln(rt.writer, rt.ui.statusKV("last_edit_loop", loop.ID))
			fmt.Fprintln(rt.writer, rt.ui.statusKV("last_edit_loop_status", valueOrUnset(loop.Status)))
			fmt.Fprintln(rt.writer, rt.ui.statusKV("last_verification", valueOrUnset(loop.VerificationStatus)))
			fmt.Fprintln(rt.writer, rt.ui.statusKV("last_remaining_risks", fmt.Sprintf("%d", len(loop.RemainingRisks))))
		}
		if rt.session.LastSelection != nil && rt.session.LastSelection.HasSelection() {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("selection", rt.session.LastSelection.Summary(rt.workspace.Root)))
		}
		rt.printRuntimeGateStatus(runtimeGateActionFinalAnswer)
		fmt.Fprintln(rt.writer)
		rt.printKVGroup("Data",
			kv("memory_files", fmt.Sprintf("%d", len(rt.memory.Files))),
			kv("persistent_memory", fmt.Sprintf("%d", rt.persistentMemoryCount())),
			kv("evidence_records", fmt.Sprintf("%d", rt.evidenceCount())),
		)
		automationSummary := summarizeAutomations(rt.session.Automations, time.Now())
		if automationSummary.Total > 0 {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("automations", automationSummaryLine(automationSummary)))
		}
		if rt.investigations != nil {
			if count, active, last, err := rt.investigations.Stats(rt.workspace.BaseRoot); err == nil {
				fmt.Fprintln(rt.writer, rt.ui.statusKV("investigation_sessions", fmt.Sprintf("%d", count)))
				fmt.Fprintln(rt.writer, rt.ui.statusKV("active_investigation", fmt.Sprintf("%t", active)))
				if !last.IsZero() {
					fmt.Fprintln(rt.writer, rt.ui.statusKV("last_investigation_update", last.Format(time.RFC3339)))
				}
			}
		}
		if rt.simulations != nil {
			if count, last, err := rt.simulations.Stats(rt.workspace.BaseRoot); err == nil {
				fmt.Fprintln(rt.writer, rt.ui.statusKV("simulation_results", fmt.Sprintf("%d", count)))
				if !last.IsZero() {
					fmt.Fprintln(rt.writer, rt.ui.statusKV("last_simulation", last.Format(time.RFC3339)))
				}
			}
		}
		fmt.Fprintln(rt.writer)
		rt.printKVGroup("Approvals",
			kv("write_approval", sessionApprovalStateLabel(rt.alwaysApproveWrites, "auto-approve")),
			kv("diff_preview", sessionApprovalStateLabel(rt.alwaysApprovePreview, "auto-accept")),
			kv("shell_approval", sessionApprovalStateLabel(rt.perms != nil && rt.perms.IsShellAllowed(), "allowed")),
			kv("git_approval", sessionApprovalStateLabel(rt.perms != nil && rt.perms.IsGitAllowed(), "allowed")),
		)
		if rt.hookOverrides != nil {
			if items, err := rt.hookOverrides.List(rt.workspace.BaseRoot); err == nil {
				fmt.Fprintln(rt.writer)
				fmt.Fprintln(rt.writer, rt.ui.subsection("Overrides"))
				fmt.Fprintln(rt.writer, rt.ui.statusKV("hook_overrides", fmt.Sprintf("%d", len(items))))
				for _, item := range items {
					line := fmt.Sprintf("%s  expires=%s", item.RuleID, item.ExpiresAt.Format(time.RFC3339))
					if strings.TrimSpace(item.Reason) != "" {
						line += "  " + item.Reason
					}
					fmt.Fprintln(rt.writer, rt.ui.dim(line))
				}
			}
		}
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, rt.ui.subsection("Extensions"))
		if rt.session.LastVerification != nil {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("last_verification", rt.session.LastVerification.SummaryLine()))
		}
		fmt.Fprintln(rt.writer, rt.ui.statusKV("verification_history", fmt.Sprintf("%d", rt.verificationHistoryCount())))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("skills", fmt.Sprintf("%d", rt.skills.Count())))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("enabled_skills", fmt.Sprintf("%d", rt.skills.EnabledCount())))
		mcpStatuses := rt.mcpStatus()
		fmt.Fprintln(rt.writer, rt.ui.statusKV("mcp_servers", fmt.Sprintf("%d", len(mcpStatuses))))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("mcp_tools", fmt.Sprintf("%d", rt.mcpToolCount())))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("mcp_resources", fmt.Sprintf("%d", rt.mcpResourceCount())))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("mcp_prompts", fmt.Sprintf("%d", rt.mcpPromptCount())))
		for _, warning := range append(append(append([]string(nil), rt.skillWarns...), rt.mcpWarns...), rt.hookWarns...) {
			fmt.Fprintln(rt.writer, rt.ui.warnLine(warning))
		}
		if rt.clientErr != nil {
			fmt.Fprintln(rt.writer, rt.ui.errorLine("provider error: "+rt.clientErr.Error()))
		}
	case "version":
		fmt.Fprintln(rt.writer, rt.ui.infoLine("Version: "+currentVersion()))
	case "model":
		if err := rt.handleModelCommand(cmd.Args); err != nil {
			return false, err
		}
	case "effort":
		if err := rt.handleEffortCommand(cmd.Args); err != nil {
			return false, err
		}
	case "codex-auth":
		if err := rt.handleOpenAICodexAuthCommand(cmd.Args); err != nil {
			return false, err
		}
	case "codex-login":
		if err := rt.handleOpenAICodexAuthCommand("login"); err != nil {
			return false, err
		}
	case "permissions":
		if cmd.Args == "" {
			fmt.Fprintln(rt.writer, rt.ui.infoLine("Permissions: "+string(rt.perms.Mode())))
			return false, nil
		}
		mode, ok := ParseModeStrict(cmd.Args)
		if !ok {
			return false, invalidPermissionModeError("permission mode", cmd.Args)
		}
		rt.perms.SetMode(mode)
		rt.session.PermissionMode = string(mode)
		rt.cfg.PermissionMode = string(mode)
		_ = rt.store.Save(rt.session)
		if err := rt.saveUserConfig(); err != nil {
			return false, err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Permissions set to "+string(mode)))
	case "set-max-tool-iterations":
		if cmd.Args == "" {
			fmt.Fprintln(rt.writer, rt.ui.infoLine("max_tool_iterations: "+formatMaxToolIterations(configMaxToolIterations(rt.cfg))))
			return false, nil
		}
		argText := strings.TrimSpace(cmd.Args)
		var val int
		if strings.EqualFold(argText, "unlimited") || strings.EqualFold(argText, "none") || strings.EqualFold(argText, "off") {
			val = 0
		} else {
			parsed, err := strconv.Atoi(argText)
			if err != nil || parsed < 0 {
				return false, fmt.Errorf("invalid value: must be a non-negative integer (0 or \"unlimited\" disables the cap)")
			}
			val = parsed
		}
		rt.cfg.MaxToolIterations = val
		if err := rt.saveUserConfig(); err != nil {
			return false, err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("max_tool_iterations set to "+formatMaxToolIterations(val)))
	case "progress-display":
		if err := rt.handleProgressDisplayCommand(cmd.Args); err != nil {
			return false, err
		}
	case "verify":
		if err := rt.handleVerifyCommand(cmd.Args); err != nil {
			return false, err
		}
	case "specialists":
		if err := rt.handleSpecialistsCommand(cmd.Args); err != nil {
			return false, err
		}
	case "suggest":
		if err := rt.handleSuggestCommand(cmd.Args); err != nil {
			return false, err
		}
	case "suggest-dashboard-html":
		if err := rt.handleSuggestDashboardHTMLCommand(cmd.Args); err != nil {
			return false, err
		}
	case "session-dashboard-html":
		if err := rt.handleSessionDashboardHTMLCommand(cmd.Args); err != nil {
			return false, err
		}
	case "events":
		if err := rt.handleEventsCommand(cmd.Args); err != nil {
			return false, err
		}
	case "continuity":
		if err := rt.handleContinuityCommand(cmd.Args); err != nil {
			return false, err
		}
	case "completion-audit":
		if err := rt.handleCompletionAuditCommand(cmd.Args); err != nil {
			return false, err
		}
	case "review":
		if err := rt.handleReviewCommand(cmd.Args); err != nil {
			return false, err
		}
	case "recover":
		if err := rt.handleRecoverCommand(cmd.Args); err != nil {
			return false, err
		}
	case "jobs":
		if err := rt.handleJobsCommand(cmd.Args); err != nil {
			return false, err
		}
	case "automation":
		if err := rt.handleAutomationCommand(cmd.Args); err != nil {
			return false, err
		}
	case "goal", "goals":
		if err := rt.handleGoalCommand(cmd.Args); err != nil {
			return false, err
		}
	case "verify-dashboard":
		if err := rt.handleVerifyDashboardCommand(cmd.Args); err != nil {
			return false, err
		}
	case "verify-dashboard-html":
		if err := rt.handleVerifyDashboardHTMLCommand(cmd.Args); err != nil {
			return false, err
		}
	case "override":
		if err := rt.handleHookOverridesCommand(); err != nil {
			return false, err
		}
	case "override-add":
		if err := rt.handleHookOverrideAddCommand(cmd.Args); err != nil {
			return false, err
		}
	case "override-clear":
		if err := rt.handleHookOverrideClearCommand(cmd.Args); err != nil {
			return false, err
		}
	case "clear", "reset", "new":
		rt.session.Messages = nil
		rt.session.Summary = ""
		rt.session.ClearSharedPlan()
		_ = rt.store.Save(rt.session)
		fmt.Fprintln(rt.writer, rt.ui.successLine("Conversation cleared"))
	case "compact":
		fmt.Fprintln(rt.writer, rt.ui.section("Compact"))
		summary, err := rt.agent.CompactWithTrigger(context.Background(), cmd.Args, "manual", "user_requested")
		if err != nil {
			return false, err
		}
		fmt.Fprintln(rt.writer, summary)
	case "context":
		fmt.Fprintln(rt.writer, rt.ui.section("Context"))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("approx_chars", fmt.Sprintf("%d", rt.session.ApproxChars())))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("messages", fmt.Sprintf("%d", len(rt.session.Messages))))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("summary_chars", fmt.Sprintf("%d", len(rt.session.Summary))))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("plan_items", fmt.Sprintf("%d", len(rt.session.Plan))))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("memory_files", fmt.Sprintf("%d", len(rt.memory.Files))))
	case "memory":
		if len(rt.memory.Files) == 0 {
			fmt.Fprintln(rt.writer, rt.ui.warnLine("No memory files loaded."))
			return false, nil
		}
		fmt.Fprintln(rt.writer, rt.ui.section("Memory"))
		for _, file := range rt.memory.Files {
			fmt.Fprintln(rt.writer, rt.ui.dim(file.Path))
		}
	case "mem":
		if err := rt.handlePersistentMemoryRecent(cmd.Args); err != nil {
			return false, err
		}
	case "evidence":
		if err := rt.handleEvidenceRecent(cmd.Args); err != nil {
			return false, err
		}
	case "evidence-search":
		if err := rt.handleEvidenceSearch(cmd.Args); err != nil {
			return false, err
		}
	case "evidence-show":
		if err := rt.handleEvidenceShow(cmd.Args); err != nil {
			return false, err
		}
	case "evidence-dashboard":
		if err := rt.handleEvidenceDashboard(cmd.Args, false); err != nil {
			return false, err
		}
	case "evidence-dashboard-html":
		if err := rt.handleEvidenceDashboard(cmd.Args, true); err != nil {
			return false, err
		}
	case "investigate":
		if err := rt.handleInvestigateCommand(cmd.Args); err != nil {
			return false, err
		}
	case "investigate-dashboard":
		if err := rt.handleInvestigationDashboard(false); err != nil {
			return false, err
		}
	case "investigate-dashboard-html":
		if err := rt.handleInvestigationDashboard(true); err != nil {
			return false, err
		}
	case "simulate":
		if err := rt.handleSimulateCommand(cmd.Args); err != nil {
			return false, err
		}
	case "fuzz-func":
		if err := rt.handleFuzzFuncCommand(cmd.Args); err != nil {
			return false, err
		}
	case "fuzz-campaign":
		if err := rt.handleFuzzCampaignCommand(cmd.Args); err != nil {
			return false, err
		}
	case "source-scan":
		if err := rt.handleSourceScanCommand(cmd.Args); err != nil {
			return false, err
		}
	case "create-driver-poc":
		if err := rt.handleCreateDriverPOCCommand(cmd.Args); err != nil {
			return false, err
		}
	case "find-root-cause":
		if err := rt.handleFindRootCauseCommand(cmd.Args); err != nil {
			return false, err
		}
	case "root-cause-patterns":
		if err := rt.handleRootCausePatternsCommand(cmd.Args); err != nil {
			return false, err
		}
	case "simulate-dashboard":
		if err := rt.handleSimulationDashboard(false); err != nil {
			return false, err
		}
	case "simulate-dashboard-html":
		if err := rt.handleSimulationDashboard(true); err != nil {
			return false, err
		}
	case "mem-search":
		if err := rt.handlePersistentMemorySearch(cmd.Args); err != nil {
			return false, err
		}
	case "mem-show":
		if err := rt.handlePersistentMemoryShow(cmd.Args); err != nil {
			return false, err
		}
	case "mem-promote":
		if err := rt.handlePersistentMemoryAdjust(cmd.Args, "promote"); err != nil {
			return false, err
		}
	case "mem-demote":
		if err := rt.handlePersistentMemoryAdjust(cmd.Args, "demote"); err != nil {
			return false, err
		}
	case "mem-confirm":
		if err := rt.handlePersistentMemoryAdjust(cmd.Args, "confirm"); err != nil {
			return false, err
		}
	case "mem-tentative":
		if err := rt.handlePersistentMemoryAdjust(cmd.Args, "tentative"); err != nil {
			return false, err
		}
	case "mem-dashboard":
		if err := rt.handlePersistentMemoryDashboard(cmd.Args, false); err != nil {
			return false, err
		}
	case "mem-dashboard-html":
		if err := rt.handlePersistentMemoryDashboard(cmd.Args, true); err != nil {
			return false, err
		}
	case "mem-prune":
		if err := rt.handlePersistentMemoryPrune(cmd.Args); err != nil {
			return false, err
		}
	case "mem-stats":
		if err := rt.handlePersistentMemoryStats(); err != nil {
			return false, err
		}
	case "checkpoint":
		if err := rt.handleCheckpointCommand(cmd.Args); err != nil {
			return false, err
		}
	case "checkpoint-auto":
		if err := rt.handleCheckpointAutoCommand(cmd.Args); err != nil {
			return false, err
		}
	case "set-msbuild-path":
		if err := rt.handleSetVerificationToolPathCommand("msbuild", cmd.Args); err != nil {
			return false, err
		}
	case "clear-msbuild-path":
		if err := rt.handleClearVerificationToolPathCommand("msbuild"); err != nil {
			return false, err
		}
	case "set-cmake-path":
		if err := rt.handleSetVerificationToolPathCommand("cmake", cmd.Args); err != nil {
			return false, err
		}
	case "clear-cmake-path":
		if err := rt.handleClearVerificationToolPathCommand("cmake"); err != nil {
			return false, err
		}
	case "set-ctest-path":
		if err := rt.handleSetVerificationToolPathCommand("ctest", cmd.Args); err != nil {
			return false, err
		}
	case "clear-ctest-path":
		if err := rt.handleClearVerificationToolPathCommand("ctest"); err != nil {
			return false, err
		}
	case "set-ninja-path":
		if err := rt.handleSetVerificationToolPathCommand("ninja", cmd.Args); err != nil {
			return false, err
		}
	case "clear-ninja-path":
		if err := rt.handleClearVerificationToolPathCommand("ninja"); err != nil {
			return false, err
		}
	case "detect-verification-tools":
		if err := rt.handleDetectVerificationToolsCommand(); err != nil {
			return false, err
		}
	case "set-auto-verify":
		if err := rt.handleSetAutoVerifyCommand(cmd.Args); err != nil {
			return false, err
		}
	case "locale-auto":
		if err := rt.handleLocaleAutoCommand(cmd.Args); err != nil {
			return false, err
		}
	case "worktree":
		if err := rt.handleWorktreeCommand(cmd.Args); err != nil {
			return false, err
		}
	case "checkpoint-diff":
		if err := rt.handleCheckpointDiffCommand(cmd.Args); err != nil {
			return false, err
		}
	case "checkpoints":
		if err := rt.handleCheckpointsCommand(); err != nil {
			return false, err
		}
	case "rollback":
		if err := rt.handleRollbackCommand(cmd.Args); err != nil {
			return false, err
		}
	case "skills":
		items := rt.skills.Items()
		if len(items) == 0 {
			fmt.Fprintln(rt.writer, rt.ui.warnLine("No skills discovered."))
			return false, nil
		}
		fmt.Fprintln(rt.writer, rt.ui.section("Skills"))
		for _, skill := range items {
			label := skill.Name
			if skill.Enabled {
				label += " [enabled]"
			}
			fmt.Fprintln(rt.writer, label)
			fmt.Fprintln(rt.writer, rt.ui.dim("  "+skill.Path))
			if strings.TrimSpace(skill.Summary) != "" {
				fmt.Fprintln(rt.writer, rt.ui.dim("  "+skill.Summary))
			}
		}
	case "mcp":
		statuses := rt.mcpStatus()
		if len(statuses) == 0 {
			fmt.Fprintln(rt.writer, rt.ui.warnLine("No MCP servers configured."))
			return false, nil
		}
		fmt.Fprintln(rt.writer, rt.ui.section("MCP"))
		for _, status := range statuses {
			environmentID := mcpStatusEnvironmentID(status)
			if strings.TrimSpace(status.Error) != "" {
				fmt.Fprintf(rt.writer, "%s  transport=%s  env=%s  error=%s\n", status.Name, valueOrDefault(status.Transport, "stdio"), environmentID, status.Error)
				continue
			}
			extra := ""
			if rt.mcp != nil {
				if server, ok := rt.mcp.ServerConfig(status.Name); ok {
					extra = webResearchMCPStatusSummary(server, os.Getenv)
				}
			}
			location := status.Cwd
			locationLabel := "cwd"
			if strings.TrimSpace(status.URL) != "" {
				location = status.URL
				locationLabel = "url"
			}
			fmt.Fprintf(rt.writer, "%s  tools=%d  resources=%d  prompts=%d  transport=%s  env=%s  %s=%s%s\n", status.Name, status.ToolCount, status.ResourceCount, status.PromptCount, valueOrDefault(status.Transport, "stdio"), environmentID, locationLabel, location, extra)
		}
	case "resources":
		items := rt.mcpResources()
		if len(items) == 0 {
			fmt.Fprintln(rt.writer, rt.ui.warnLine("No MCP resources discovered."))
			return false, nil
		}
		fmt.Fprintln(rt.writer, rt.ui.section("Resources"))
		for _, item := range items {
			label := item.Resource.URI
			if label == "" {
				label = item.Resource.Name
			}
			line := fmt.Sprintf("%s:%s", item.Server, label)
			if item.Resource.Name != "" && item.Resource.Name != label {
				line += " (" + item.Resource.Name + ")"
			}
			fmt.Fprintln(rt.writer, line)
			if strings.TrimSpace(item.Resource.Description) != "" {
				fmt.Fprintln(rt.writer, rt.ui.dim("  "+item.Resource.Description))
			}
		}
	case "resource":
		if strings.TrimSpace(cmd.Args) == "" {
			return false, fmt.Errorf("usage: /resource <server:resource-uri-or-name>")
		}
		if rt.mcp == nil {
			return false, fmt.Errorf("no MCP servers configured")
		}
		display, text, err := rt.mcp.ReadResource(context.Background(), cmd.Args)
		if err != nil {
			return false, err
		}
		fmt.Fprintln(rt.writer, rt.ui.section("Resource"))
		fmt.Fprintln(rt.writer, rt.ui.dim(display))
		fmt.Fprintln(rt.writer, text)
	case "prompts":
		items := rt.mcpPrompts()
		if len(items) == 0 {
			fmt.Fprintln(rt.writer, rt.ui.warnLine("No MCP prompts discovered."))
			return false, nil
		}
		fmt.Fprintln(rt.writer, rt.ui.section("Prompts"))
		for _, item := range items {
			args := []string{}
			for _, arg := range item.Prompt.Arguments {
				label := arg.Name
				if arg.Required {
					label += "*"
				}
				args = append(args, label)
			}
			fmt.Fprintf(rt.writer, "%s:%s(%s)\n", item.Server, item.Prompt.Name, strings.Join(args, ", "))
			if strings.TrimSpace(item.Prompt.Description) != "" {
				fmt.Fprintln(rt.writer, rt.ui.dim("  "+item.Prompt.Description))
			}
		}
	case "prompt":
		if strings.TrimSpace(cmd.Args) == "" {
			return false, fmt.Errorf("usage: /prompt <server:prompt-name> [json-arguments]")
		}
		if rt.mcp == nil {
			return false, fmt.Errorf("no MCP servers configured")
		}
		target, args, err := parsePromptCommandArgs(cmd.Args)
		if err != nil {
			return false, err
		}
		display, text, err := rt.mcp.GetPrompt(context.Background(), target, args)
		if err != nil {
			return false, err
		}
		fmt.Fprintln(rt.writer, rt.ui.section("Prompt"))
		fmt.Fprintln(rt.writer, rt.ui.dim(display))
		fmt.Fprintln(rt.writer, text)
	case "reload":
		if err := rt.reloadRuntimeConfig(); err != nil {
			return false, err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Reloaded config, memory, skills, hooks, and MCP extensions"))
	case "hook-reload":
		rt.reloadHooks()
		fmt.Fprintln(rt.writer, rt.ui.successLine("Reloaded hook configuration"))
	case "hooks":
		rt.handleHooksCommand()
	case "init":
		if err := rt.handleInitCommand(cmd.Args); err != nil {
			return false, err
		}
	case "open":
		if err := rt.handleOpenCommand(cmd.Args); err != nil {
			return false, err
		}
	case "selection":
		if err := rt.handleSelectionCommand(); err != nil {
			return false, err
		}
	case "selections":
		if err := rt.handleSelectionsCommand(); err != nil {
			return false, err
		}
	case "use-selection":
		if err := rt.handleUseSelectionCommand(cmd.Args); err != nil {
			return false, err
		}
	case "drop-selection":
		if err := rt.handleDropSelectionCommand(cmd.Args); err != nil {
			return false, err
		}
	case "note-selection":
		if err := rt.handleSelectionNoteCommand(cmd.Args); err != nil {
			return false, err
		}
	case "tag-selection":
		if err := rt.handleSelectionTagCommand(cmd.Args); err != nil {
			return false, err
		}
	case "clear-selection":
		if current := rt.session.ActiveSelection; !rt.session.RemoveSelection(current) {
			rt.session.ClearSelections()
		}
		_ = rt.store.Save(rt.session)
		fmt.Fprintln(rt.writer, rt.ui.successLine("Cleared current selection"))
	case "clear-selections":
		rt.session.ClearSelections()
		_ = rt.store.Save(rt.session)
		fmt.Fprintln(rt.writer, rt.ui.successLine("Cleared all selections"))
	case "diff-selection":
		if err := rt.handleSelectionDiffCommand(); err != nil {
			return false, err
		}
	case "edit-selection":
		if err := rt.handleSelectionEditCommand(cmd.Args); err != nil {
			return false, err
		}
	case "resume":
		if cmd.Args == "" {
			return false, fmt.Errorf("usage: /resume <session-id>")
		}
		loaded, err := rt.store.Load(cmd.Args)
		if err != nil {
			return false, err
		}
		rt.session = loaded
		rt.cfg.Provider = loaded.Provider
		rt.cfg.Model = loaded.Model
		rt.cfg.BaseURL = loaded.BaseURL
		rt.perms.SetMode(ParseMode(loaded.PermissionMode))
		if err := rt.reloadSessionContext(); err != nil {
			return false, err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Resumed %s (%s)", loaded.ID, loaded.Name)))
	case "rename":
		if cmd.Args == "" {
			return false, fmt.Errorf("usage: /rename <name>")
		}
		rt.session.Name = cmd.Args
		_ = rt.store.Save(rt.session)
		fmt.Fprintln(rt.writer, rt.ui.successLine("Session renamed to "+cmd.Args))
	case "session":
		fmt.Fprintln(rt.writer, rt.ui.section("Session"))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("session_id", rt.session.ID))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("name", rt.session.Name))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("stored_at", filepath.Join(rt.store.Root(), rt.session.ID+".json")))
	case "sessions":
		sessionArgs := strings.TrimSpace(cmd.Args)
		if sessionArgs != "" {
			query := sessionArgs
			parts := strings.SplitN(sessionArgs, " ", 2)
			if len(parts) > 0 {
				action := normalizeSlashCommandName(parts[0])
				if action == "search" || action == "find" {
					if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
						return false, fmt.Errorf("usage: /sessions search <query>")
					}
					query = strings.TrimSpace(parts[1])
				}
			}
			items, err := rt.store.Search(query, 20)
			if err != nil {
				return false, err
			}
			if len(items) == 0 {
				fmt.Fprintln(rt.writer, rt.ui.warnLine("No sessions matched query."))
				return false, nil
			}
			fmt.Fprintln(rt.writer, rt.ui.section("Session Search"))
			fmt.Fprintln(rt.writer, rt.ui.statusKV("query", query))
			for _, item := range items {
				fmt.Fprintf(rt.writer, "%s  %s  %s\n", rt.ui.dim(item.ID), rt.ui.info(item.UpdatedAt.Format(time.RFC3339)), item.Name)
				if strings.TrimSpace(item.WorkingDir) != "" {
					fmt.Fprintf(rt.writer, "  %s %s\n", rt.ui.dim("cwd:"), rt.ui.dim(item.WorkingDir))
				}
				if strings.TrimSpace(item.Snippet) != "" {
					fmt.Fprintf(rt.writer, "  %s %s\n", rt.ui.dim(item.MatchField+":"), item.Snippet)
				}
			}
			return false, nil
		}
		items, err := rt.store.List()
		if err != nil {
			return false, err
		}
		if len(items) == 0 {
			fmt.Fprintln(rt.writer, rt.ui.warnLine("No saved sessions."))
			return false, nil
		}
		fmt.Fprintln(rt.writer, rt.ui.section("Sessions"))
		for _, item := range items {
			fmt.Fprintf(rt.writer, "%s  %s  %s\n", rt.ui.dim(item.ID), rt.ui.info(item.UpdatedAt.Format(time.RFC3339)), item.Name)
		}
	case "handoff":
		if err := rt.handleDelegationHandoffCommand(cmd.Args); err != nil {
			return false, err
		}
	case "tasks":
		if len(rt.session.Plan) == 0 {
			if rt.session.TaskGraph != nil && len(rt.session.TaskGraph.Nodes) > 0 {
				fmt.Fprintln(rt.writer, rt.ui.section("Tasks"))
				fmt.Fprintln(rt.writer, rt.session.TaskGraph.RenderExportSection())
				return false, nil
			}
			fmt.Fprintln(rt.writer, rt.ui.warnLine("No active plan."))
			return false, nil
		}
		fmt.Fprintln(rt.writer, rt.ui.section("Tasks"))
		var completed int
		var inProgress int
		var pending int
		for _, item := range rt.session.Plan {
			switch item.Status {
			case "completed":
				completed++
			case "in_progress":
				inProgress++
			default:
				pending++
			}
		}
		fmt.Fprintln(rt.writer)
		rt.printKVGroup("Summary",
			kv("total", fmt.Sprintf("%d", len(rt.session.Plan))),
			kv("completed", fmt.Sprintf("%d", completed)),
			kv("in_progress", fmt.Sprintf("%d", inProgress)),
			kv("pending", fmt.Sprintf("%d", pending)),
		)
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, rt.ui.subsection("Plan"))
		for i, item := range rt.session.Plan {
			fmt.Fprintln(rt.writer, rt.ui.planItem(i, item.Status, item.Step))
		}
	case "provider":
		return false, rt.handleProviderCommand(cmd.Args)
	case "profile":
		return false, rt.handleProfileCommand(cmd.Args)
	case "diff":
		out, err := NewGitDiffTool(rt.workspace).Execute(context.Background(), map[string]any{})
		if err == nil {
			if viewErr := rt.presentDiffView("Workspace Diff", rt.workspace.Root, out); viewErr == nil {
				fmt.Fprintln(rt.writer, rt.ui.successLine("Opened workspace diff in internal diff view"))
				return false, nil
			}
		}
		if strings.TrimSpace(out) != "" {
			fmt.Fprintln(rt.writer, out)
		}
		return false, err
	case "export":
		text := rt.session.ExportText()
		if cmd.Args == "" {
			fmt.Fprintln(rt.writer, text)
			return false, nil
		}
		if err := os.WriteFile(cmd.Args, []byte(text), 0o644); err != nil {
			return false, err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Exported to "+cmd.Args))
	case "config":
		fmt.Fprintln(rt.writer, rt.ui.section("Config"))
		fmt.Fprintln(rt.writer, rt.ui.hintLine("Effective settings merged from user config, trusted project config, environment, and session overrides."))
		fmt.Fprintln(rt.writer)
		rt.printKVGroup("Model",
			kv("provider", valueOrUnset(rt.cfg.Provider)),
			kv("model", valueOrUnset(rt.cfg.Model)),
			kv("main_reasoning_effort", reasoningEffortDisplay(rt.cfg.ReasoningEffort)),
			kv("max_tokens", fmt.Sprintf("%d", rt.cfg.MaxTokens)),
			kv("base_url", valueOrUnset(rt.cfg.BaseURL)),
		)
		fmt.Fprintln(rt.writer)
		routePolicy := modelRoutePolicyFromConfig(rt.cfg)
		rt.printKVGroup("Model Route Scheduler",
			kv("enabled", fmt.Sprintf("%t", routePolicy.Enabled)),
			kv("default_max_concurrent", fmt.Sprintf("%d", routePolicy.DefaultMaxConcurrent)),
			kv("provider_limits", fmt.Sprintf("%d", len(routePolicy.ProviderLimits))),
			kv("active_routes", fmt.Sprintf("%d", len(rt.modelRoutes.Snapshot()))),
		)
		fmt.Fprintln(rt.writer)
		rt.printKVGroup("Runtime",
			kv("shell", valueOrUnset(rt.cfg.Shell)),
			kv("permission_mode", string(rt.perms.Mode())),
			kv("active_permission_profile", valueOrUnset(activePermissionProfileIDForMode(rt.perms.Mode()))),
			kv("session_dir", rt.cfg.SessionDir),
			kv("progress_display", configProgressDisplay(rt.cfg)),
			kv("max_tool_iterations", formatMaxToolIterations(configMaxToolIterations(rt.cfg))),
			kv("auto_compact_chars", fmt.Sprintf("%d", rt.cfg.AutoCompactChars)),
		)
		fmt.Fprintln(rt.writer)
		projectTrustLevel := normalizeProjectTrustLevel(projectTrustLevelForPath(rt.cfg, rt.workspace.BaseRoot))
		if projectTrustLevel == "" {
			projectTrustLevel = "unknown"
		}
		projectLocalState := "ignored"
		if strings.EqualFold(projectTrustLevel, "trusted") {
			projectLocalState = "eligible"
		}
		rt.printKVGroup("Project Trust",
			kv("trust_level", projectTrustLevel),
			kv("project_config", projectLocalState),
			kv("project_hooks", projectLocalState),
		)
		fmt.Fprintln(rt.writer)
		rt.printKVGroup("Automation",
			kv("auto_checkpoint_edits", fmt.Sprintf("%t", configAutoCheckpointEdits(rt.cfg))),
			kv("auto_verify", fmt.Sprintf("%t", configAutoVerify(rt.cfg))),
			kv("auto_locale", fmt.Sprintf("%t", configAutoLocale(rt.cfg))),
			kv("fuzz_func_output_language", functionFuzzOutputLanguageSummary(rt.cfg)),
		)
		fmt.Fprintln(rt.writer)
		rt.printKVGroup("Hooks",
			kv("hooks_enabled", fmt.Sprintf("%t", configHooksEnabled(rt.cfg))),
			kv("hook_presets", fmt.Sprintf("%d", len(rt.cfg.HookPresets))),
			kv("hook_rules", fmt.Sprintf("%d", rt.hookRuleCount())),
		)
		fmt.Fprintln(rt.writer)
		rt.printKVGroup("Specialists",
			kv("enabled", fmt.Sprintf("%t", configSpecialistsEnabled(rt.cfg))),
			kv("profiles", fmt.Sprintf("%d", len(configuredSpecialistProfiles(rt.cfg)))),
		)
		fmt.Fprintln(rt.writer)
		rt.printKVGroup("Worktree Isolation",
			kv("enabled", fmt.Sprintf("%t", configWorktreeIsolationEnabled(rt.cfg))),
			kv("root_dir", configWorktreeIsolationRootDir(rt.cfg)),
			kv("branch_prefix", configWorktreeIsolationBranchPrefix(rt.cfg)),
			kv("auto_for_tracked_features", fmt.Sprintf("%t", configWorktreeIsolationAutoForTrackedFeatures(rt.cfg))),
		)
		fmt.Fprintln(rt.writer)
		rt.printKVGroup("Tool Paths",
			kv("msbuild_path", valueOrUnset(rt.cfg.MSBuildPath)),
			kv("cmake_path", valueOrUnset(rt.cfg.CMakePath)),
			kv("ctest_path", valueOrUnset(rt.cfg.CTestPath)),
			kv("ninja_path", valueOrUnset(rt.cfg.NinjaPath)),
		)
		fmt.Fprintln(rt.writer)
		rt.printKVGroup("Extensions",
			kv("skill_paths", fmt.Sprintf("%d", len(rt.cfg.SkillPaths))),
			kv("enabled_skills", strings.Join(rt.cfg.EnabledSkills, ", ")),
			kv("mcp_servers", fmt.Sprintf("%d", len(rt.cfg.MCPServers))),
		)
		if items := webResearchConfigKVs(rt.cfg, os.Getenv); len(items) > 0 {
			fmt.Fprintln(rt.writer)
			rt.printKVGroup("Web Research MCP", items...)
		}
		if rt.longMem != nil {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("persistent_memory_path", rt.longMem.Path))
		}
		if rt.checkpoints != nil {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("checkpoint_root", rt.checkpoints.Root))
		}
	case "trust":
		if err := rt.handleTrustCommand(cmd.Args); err != nil {
			return false, err
		}
	case "set-analysis-models":
		if err := rt.handleSetAnalysisModelsCommand(cmd.Args); err != nil {
			return false, err
		}
	case "set-specialist-model":
		if err := rt.handleSetSpecialistModelCommand(cmd.Args); err != nil {
			return false, err
		}
	case "new-feature":
		if err := rt.handleNewFeatureCommand(cmd.Args); err != nil {
			return false, err
		}
	case "analyze-project":
		if err := rt.handleAnalyzeProjectCommand(cmd.Args); err != nil {
			return false, err
		}
	case "analyze-dashboard":
		if err := rt.handleAnalyzeDashboardCommand(cmd.Args); err != nil {
			return false, err
		}
	case "docs-refresh":
		if err := rt.handleDocsRefreshCommand(cmd.Args); err != nil {
			return false, err
		}
	case "analyze-performance":
		if err := rt.handleAnalyzePerformanceCommand(cmd.Args); err != nil {
			return false, err
		}
	case "exit", "quit":
		return true, nil
	default:
		if suggestion := removedReviewCommandSuggestion(cmd.Name); suggestion != "" {
			return false, fmt.Errorf("unknown command: /%s. %s", cmd.Name, suggestion)
		}
		return false, fmt.Errorf("unknown command: /%s", cmd.Name)
	}
	return false, nil
}

func removedReviewCommandSuggestion(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "review-pr":
		return "Use /review pr instead."
	case "review-selection":
		return "Use /review selection instead."
	case "review-selections":
		return "Use /review selection --all instead."
	case "do-plan-review":
		return "Use /review plan instead."
	case "set-plan-review":
		return "Use /review models design instead."
	case "profile-review":
		return "Use /review models or /review models <role> instead."
	default:
		return ""
	}
}

func (rt *runtimeState) handleLocaleAutoCommand(args string) error {
	if args == "" {
		fmt.Fprintln(rt.writer, rt.ui.infoLine(fmt.Sprintf("Auto-locale is currently: %t", configAutoLocale(rt.cfg))))
		return nil
	}
	val, ok := parseBoolString(args)
	if !ok {
		return fmt.Errorf("usage: /locale-auto [on|off]")
	}
	rt.cfg.AutoLocale = boolPtr(val)
	if err := rt.saveUserConfig(); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Auto-locale set to %t", val)))
	return nil
}

func (rt *runtimeState) handleSetAutoVerifyCommand(args string) error {
	if args == "" {
		fmt.Fprintln(rt.writer, rt.ui.infoLine(fmt.Sprintf("Automatic verification is currently: %t", configAutoVerify(rt.cfg))))
		return nil
	}
	val, ok := parseBoolString(args)
	if !ok {
		return fmt.Errorf("usage: /set-auto-verify [on|off]")
	}
	rt.cfg.AutoVerify = boolPtr(val)
	rt.syncClientFromConfig()
	if err := rt.saveUserConfig(); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Automatic verification set to %t", val)))
	return nil
}

func (rt *runtimeState) handleProgressDisplayCommand(args string) error {
	if strings.TrimSpace(args) == "" {
		fmt.Fprintln(rt.writer, rt.ui.infoLine("progress_display: "+configProgressDisplay(rt.cfg)))
		return nil
	}
	normalized, ok := parseProgressDisplayInput(args)
	if !ok || strings.TrimSpace(args) == "" {
		return fmt.Errorf("usage: /progress-display [auto|compact|stream]")
	}
	rt.cfg.ProgressDisplay = normalized
	if err := rt.saveUserConfig(); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("progress_display set to "+normalized))
	return nil
}

func (rt *runtimeState) handleSetVerificationToolPathCommand(toolName, args string) error {
	displayName := suggestedVerificationToolDisplayName(toolName)
	current := currentVerificationToolPath(rt.cfg, toolName)
	if strings.TrimSpace(args) == "" {
		suggested := suggestedVerificationToolPath(rt.cfg, toolName)
		fmt.Fprintln(rt.writer, rt.ui.infoLine(fmt.Sprintf("%s path: %s", displayName, valueOrUnset(current))))
		if strings.TrimSpace(suggested) != "" && !strings.EqualFold(strings.TrimSpace(suggested), strings.TrimSpace(current)) {
			fmt.Fprintln(rt.writer, rt.ui.dim("Suggested: "+suggested))
		}
		return nil
	}
	resolvedPath, err := normalizeVerificationToolPathInput(args)
	if err != nil {
		return err
	}
	configKey := verificationToolConfigKey(toolName)
	if strings.TrimSpace(configKey) == "" {
		return fmt.Errorf("unsupported verification tool: %s", toolName)
	}
	if err := SaveWorkspaceConfigOverrides(rt.workspace.BaseRoot, map[string]any{
		configKey: resolvedPath,
	}); err != nil {
		return err
	}
	rt.warnIfProjectLocalConfigIgnored()
	applyVerificationToolPathToConfig(&rt.cfg, toolName, resolvedPath)
	rt.workspace.VerificationToolPaths = buildVerificationToolPaths(rt.cfg)
	if rt.agent != nil {
		rt.agent.Config = rt.cfg
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("%s path set to %s", displayName, resolvedPath)))
	return nil
}

func (rt *runtimeState) handleClearVerificationToolPathCommand(toolName string) error {
	displayName := suggestedVerificationToolDisplayName(toolName)
	configKey := verificationToolConfigKey(toolName)
	if strings.TrimSpace(configKey) == "" {
		return fmt.Errorf("unsupported verification tool: %s", toolName)
	}
	if err := SaveWorkspaceConfigOverrides(rt.workspace.BaseRoot, map[string]any{
		configKey: nil,
	}); err != nil {
		return err
	}
	rt.warnIfProjectLocalConfigIgnored()
	applyVerificationToolPathToConfig(&rt.cfg, toolName, "")
	rt.workspace.VerificationToolPaths = buildVerificationToolPaths(rt.cfg)
	if rt.agent != nil {
		rt.agent.Config = rt.cfg
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("%s path cleared for this workspace", displayName)))
	return nil
}

func (rt *runtimeState) handleDetectVerificationToolsCommand() error {
	applied, err := autoPopulateVerificationToolPaths(rt.workspace.BaseRoot, &rt.cfg, detectWindowsVerificationToolPath)
	if err != nil {
		return err
	}
	rt.workspace.VerificationToolPaths = buildVerificationToolPaths(rt.cfg)
	if rt.agent != nil {
		rt.agent.Config = rt.cfg
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Verification Tools"))
	if len(applied) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.infoLine("No new verification tool paths were detected."))
		return nil
	}
	for _, toolName := range []string{"msbuild", "cmake", "ctest", "ninja"} {
		if path := strings.TrimSpace(applied[toolName]); path != "" {
			fmt.Fprintln(rt.writer, rt.ui.statusKV(strings.ToLower(suggestedVerificationToolDisplayName(toolName)), path))
		}
	}
	return nil
}

func buildVerificationToolPaths(cfg Config) map[string]string {
	paths := map[string]string{}
	if strings.TrimSpace(cfg.MSBuildPath) != "" {
		paths["msbuild"] = strings.TrimSpace(cfg.MSBuildPath)
	}
	if strings.TrimSpace(cfg.CMakePath) != "" {
		paths["cmake"] = strings.TrimSpace(cfg.CMakePath)
	}
	if strings.TrimSpace(cfg.CTestPath) != "" {
		paths["ctest"] = strings.TrimSpace(cfg.CTestPath)
	}
	if strings.TrimSpace(cfg.NinjaPath) != "" {
		paths["ninja"] = strings.TrimSpace(cfg.NinjaPath)
	}
	if len(paths) == 0 {
		return nil
	}
	return paths
}

func autoPopulateVerificationToolPaths(cwd string, cfg *Config, detector func(string) string) (map[string]string, error) {
	if runtime.GOOS != "windows" {
		return nil, nil
	}
	if cfg == nil || detector == nil {
		return nil, nil
	}
	updates := map[string]any{}
	applied := map[string]string{}
	for _, toolName := range []string{"msbuild", "cmake", "ctest", "ninja"} {
		if strings.TrimSpace(currentVerificationToolPath(*cfg, toolName)) != "" {
			continue
		}
		detected := strings.TrimSpace(detector(toolName))
		if detected == "" {
			continue
		}
		applyVerificationToolPathToConfig(cfg, toolName, detected)
		updates[verificationToolConfigKey(toolName)] = detected
		applied[toolName] = detected
	}
	if len(updates) == 0 {
		return nil, nil
	}
	if err := SaveWorkspaceConfigOverrides(cwd, updates); err != nil {
		return nil, err
	}
	return applied, nil
}

func currentVerificationToolPath(cfg Config, toolName string) string {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "msbuild":
		return strings.TrimSpace(cfg.MSBuildPath)
	case "cmake":
		return strings.TrimSpace(cfg.CMakePath)
	case "ctest":
		return strings.TrimSpace(cfg.CTestPath)
	case "ninja":
		return strings.TrimSpace(cfg.NinjaPath)
	default:
		return ""
	}
}

func suggestedVerificationToolDisplayName(toolName string) string {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "msbuild":
		return "MSBuild"
	case "cmake":
		return "CMake"
	case "ctest":
		return "CTest"
	case "ninja":
		return "Ninja"
	default:
		return strings.TrimSpace(toolName)
	}
}

func verificationToolConfigKey(toolName string) string {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "msbuild":
		return "msbuild_path"
	case "cmake":
		return "cmake_path"
	case "ctest":
		return "ctest_path"
	case "ninja":
		return "ninja_path"
	default:
		return ""
	}
}

func applyVerificationToolPathToConfig(cfg *Config, toolName, path string) {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "msbuild":
		cfg.MSBuildPath = path
	case "cmake":
		cfg.CMakePath = path
	case "ctest":
		cfg.CTestPath = path
	case "ninja":
		cfg.NinjaPath = path
	}
}

func normalizeVerificationToolPathInput(value string) (string, error) {
	trimmed := strings.TrimSpace(strings.Trim(value, `"'`))
	if trimmed == "" {
		return "", fmt.Errorf("tool path cannot be empty")
	}
	resolved := expandHome(trimmed)
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("tool path must point to an executable file, not a directory")
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func suggestedVerificationToolPath(cfg Config, toolName string) string {
	if current := currentVerificationToolPath(cfg, toolName); strings.TrimSpace(current) != "" {
		return current
	}
	if runtime.GOOS != "windows" {
		return ""
	}
	if discovered := detectWindowsVerificationToolPath(toolName); strings.TrimSpace(discovered) != "" {
		return discovered
	}
	return ""
}

func detectWindowsVerificationToolPath(toolName string) string {
	candidates := windowsVerificationToolCandidates(toolName)
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			abs, absErr := filepath.Abs(candidate)
			if absErr == nil {
				return abs
			}
			return candidate
		}
	}
	return ""
}

func windowsVerificationToolCandidates(toolName string) []string {
	tool := strings.ToLower(strings.TrimSpace(toolName))
	var out []string
	programFiles := []string{
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		`C:\Program Files`,
		`C:\Program Files (x86)`,
	}

	appendIf := func(path string) {
		if strings.TrimSpace(path) != "" {
			out = append(out, path)
		}
	}

	for _, base := range uniqueStrings(programFiles) {
		switch tool {
		case "cmake":
			appendIf(filepath.Join(base, "CMake", "bin", "cmake.exe"))
		case "ctest":
			appendIf(filepath.Join(base, "CMake", "bin", "ctest.exe"))
		case "ninja":
			appendIf(filepath.Join(base, "CMake", "bin", "ninja.exe"))
		}
	}

	for _, root := range windowsVisualStudioInstallRoots() {
		switch tool {
		case "msbuild":
			appendIf(filepath.Join(root, "MSBuild", "Current", "Bin", "MSBuild.exe"))
		case "cmake":
			appendIf(filepath.Join(root, "Common7", "IDE", "CommonExtensions", "Microsoft", "CMake", "CMake", "bin", "cmake.exe"))
		case "ctest":
			appendIf(filepath.Join(root, "Common7", "IDE", "CommonExtensions", "Microsoft", "CMake", "CMake", "bin", "ctest.exe"))
		case "ninja":
			appendIf(filepath.Join(root, "Common7", "IDE", "CommonExtensions", "Microsoft", "CMake", "Ninja", "ninja.exe"))
		}
	}

	return uniqueStrings(out)
}

func windowsVisualStudioInstallRoots() []string {
	var roots []string
	for _, installerBase := range []string{
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft Visual Studio", "Installer"),
		filepath.Join(os.Getenv("ProgramFiles"), "Microsoft Visual Studio", "Installer"),
		`C:\Program Files (x86)\Microsoft Visual Studio\Installer`,
		`C:\Program Files\Microsoft Visual Studio\Installer`,
	} {
		if strings.TrimSpace(installerBase) == "" {
			continue
		}
		vswhere := filepath.Join(installerBase, "vswhere.exe")
		if _, err := os.Stat(vswhere); err == nil {
			out, cmdErr := exec.Command(vswhere, "-latest", "-products", "*", "-property", "installationPath").Output()
			if cmdErr == nil {
				if path := strings.TrimSpace(string(out)); path != "" {
					roots = append(roots, path)
				}
			}
		}
	}

	for _, base := range []string{
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft Visual Studio"),
		filepath.Join(os.Getenv("ProgramFiles"), "Microsoft Visual Studio"),
		`C:\Program Files (x86)\Microsoft Visual Studio`,
		`C:\Program Files\Microsoft Visual Studio`,
	} {
		if strings.TrimSpace(base) == "" {
			continue
		}
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, yearEntry := range entries {
			if !yearEntry.IsDir() {
				continue
			}
			yearPath := filepath.Join(base, yearEntry.Name())
			skus, skuErr := os.ReadDir(yearPath)
			if skuErr != nil {
				continue
			}
			for _, skuEntry := range skus {
				if skuEntry.IsDir() {
					roots = append(roots, filepath.Join(yearPath, skuEntry.Name()))
				}
			}
		}
	}
	return uniqueStrings(roots)
}

func (rt *runtimeState) verificationToolDetector() func(string) string {
	if rt.detectVerificationToolPath != nil {
		return rt.detectVerificationToolPath
	}
	return detectWindowsVerificationToolPath
}

func (rt *runtimeState) promptConfirmAutoVerify(plan VerificationPlan) (bool, error) {
	if rt.autoApproveVerificationPrompt() {
		return true, nil
	}
	if summary := renderAutoVerificationPromptSummary(rt.cfg, plan); summary != "" {
		rt.writeOutputLines(summary)
	}
	var (
		confirmed bool
		err       error
	)
	rt.withRequestCancelSuspended(func() {
		confirmed, err = rt.confirm(autoVerificationQuestion(rt.cfg))
	})
	if err != nil {
		if !rt.interactive && (errors.Is(err, io.EOF) || errors.Is(err, ErrPromptCanceled)) {
			return false, nil
		}
		return false, err
	}
	return confirmed, nil
}

func (rt *runtimeState) autoApproveVerificationPrompt() bool {
	if rt.alwaysApproveVerification {
		return true
	}
	if rt.perms != nil && rt.perms.Mode() == ModeBypass {
		return true
	}
	if ParseMode(rt.cfg.PermissionMode) == ModeBypass {
		return true
	}
	if rt.session != nil && ParseMode(rt.session.PermissionMode) == ModeBypass {
		return true
	}
	return false
}

func renderAutoVerificationPromptSummary(cfg Config, plan VerificationPlan) string {
	var lines []string
	lines = append(lines, localizedText(cfg, "Automatic verification plan:", "자동 검증 계획:"))
	if len(plan.ChangedPaths) > 0 {
		lines = append(lines, "- "+localizedText(cfg, "Changed paths: ", "변경 경로: ")+strings.Join(limitStrings(plan.ChangedPaths, 6), ", "))
	}
	for i, step := range plan.Steps {
		if i >= 5 {
			lines = append(lines, fmt.Sprintf(localizedText(cfg, "- ... %d more step(s)", "- ... %d개 단계 더 있음"), len(plan.Steps)-i))
			break
		}
		label := strings.TrimSpace(step.Label)
		if label == "" {
			label = localizedText(cfg, "verification step", "검증 단계")
		}
		line := "- " + label
		if command := strings.TrimSpace(step.Command); command != "" {
			line += ": " + compactPromptSection(command, 160)
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (rt *runtimeState) tryAutoResolveAutoVerifyFailure(report VerificationReport) (AutoVerifyFailureResolution, bool, error) {
	if runtime.GOOS != "windows" {
		return AutoVerifyFailureNoAction, false, nil
	}
	if strings.TrimSpace(rt.workspace.BaseRoot) == "" {
		return AutoVerifyFailureNoAction, false, nil
	}
	applied, err := autoPopulateVerificationToolPaths(rt.workspace.BaseRoot, &rt.cfg, rt.verificationToolDetector())
	if err != nil {
		return AutoVerifyFailureNoAction, false, err
	}
	if len(applied) == 0 {
		return AutoVerifyFailureNoAction, false, nil
	}
	rt.warnIfProjectLocalConfigIgnored()
	rt.workspace.VerificationToolPaths = buildVerificationToolPaths(rt.cfg)
	if rt.agent != nil {
		rt.agent.Config = rt.cfg
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Verification Tools"))
	toolName := strings.ToLower(strings.TrimSpace(report.MissingCommandTool()))
	if path := strings.TrimSpace(applied[toolName]); toolName != "" && path != "" {
		fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Auto-detected %s at %s", suggestedVerificationToolDisplayName(toolName), path)))
	} else {
		fmt.Fprintln(rt.writer, rt.ui.successLine("Auto-detected verification tool path overrides for this workspace."))
		for _, candidate := range []string{"msbuild", "cmake", "ctest", "ninja"} {
			if path := strings.TrimSpace(applied[candidate]); path != "" {
				fmt.Fprintln(rt.writer, rt.ui.statusKV(strings.ToLower(suggestedVerificationToolDisplayName(candidate)), path))
			}
		}
	}
	fmt.Fprintln(rt.writer, rt.ui.infoLine("Retrying automatic verification with the detected tool paths."))
	return AutoVerifyFailureRetry, true, nil
}

func (rt *runtimeState) promptResolveAutoVerifyFailure(report VerificationReport) (AutoVerifyFailureResolution, error) {
	if resolution, handled, err := rt.tryAutoResolveAutoVerifyFailure(report); handled || err != nil {
		return resolution, err
	}
	if !rt.interactive {
		return AutoVerifyFailureNoAction, nil
	}

	toolName := report.MissingCommandTool()
	label := rt.ui.warnLine("Automatic verification failed because a verification tool could not be started.") + " " + rt.ui.dim("[p=set path/y=disable now/a=always disable for this workspace, Esc=cancel]")
	for {
		var answer string
		err := rt.withPinnedPrompt(func() error {
			var usedInteractive bool
			var err error
			answer, usedInteractive, err = rt.readInteractiveLine(label+" ", "", nil, true)
			if !usedInteractive {
				fmt.Fprint(rt.writer, label+" ")
				answer, err = rt.reader.ReadString('\n')
				if err != nil {
					return err
				}
				answer = strings.TrimSpace(answer)
				return nil
			}
			return err
		})
		if err != nil {
			if errors.Is(err, ErrPromptCanceled) {
				return AutoVerifyFailureNoAction, nil
			}
			return AutoVerifyFailureNoAction, err
		}

		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "p", "path":
			if strings.TrimSpace(toolName) == "" {
				rt.writeOutputLines(rt.ui.warnLine("Could not identify which verification tool is missing."))
				return AutoVerifyFailureNoAction, nil
			}
			configKey := verificationToolConfigKey(toolName)
			currentValue := suggestedVerificationToolPath(rt.cfg, toolName)
			value, valueErr := rt.promptValue(fmt.Sprintf("Path for %s executable", toolName), currentValue)
			if valueErr != nil {
				return AutoVerifyFailureNoAction, valueErr
			}
			resolvedPath, pathErr := normalizeVerificationToolPathInput(value)
			if pathErr != nil {
				return AutoVerifyFailureNoAction, pathErr
			}
			if err := SaveWorkspaceConfigOverrides(rt.workspace.BaseRoot, map[string]any{
				configKey: resolvedPath,
			}); err != nil {
				return AutoVerifyFailureNoAction, err
			}
			rt.warnIfProjectLocalConfigIgnored()
			applyVerificationToolPathToConfig(&rt.cfg, toolName, resolvedPath)
			rt.workspace.VerificationToolPaths = buildVerificationToolPaths(rt.cfg)
			if rt.agent != nil {
				rt.agent.Config = rt.cfg
			}
			rt.writeOutputLines(rt.ui.successLine(fmt.Sprintf("%s path saved for this workspace.", strings.ToUpper(toolName))))
			return AutoVerifyFailureRetry, nil
		case "y", "yes":
			rt.cfg.AutoVerify = boolPtr(false)
			if rt.agent != nil {
				rt.agent.Config = rt.cfg
			}
			lines := []string{rt.ui.successLine("Automatic verification disabled for this session.")}
			if summary := strings.TrimSpace(report.FailureSummary()); summary != "" {
				lines = append(lines, rt.ui.warnLine("Latest verification failure:"), rt.ui.dim(summary))
			}
			rt.writeOutputLines(lines...)
			return AutoVerifyFailureDisable, nil
		case "a", "always":
			rt.cfg.AutoVerify = boolPtr(false)
			if rt.agent != nil {
				rt.agent.Config = rt.cfg
			}
			if err := SaveWorkspaceConfigOverrides(rt.workspace.BaseRoot, map[string]any{
				"auto_verify": false,
			}); err != nil {
				return AutoVerifyFailureNoAction, err
			}
			rt.warnIfProjectLocalConfigIgnored()
			lines := []string{rt.ui.successLine("Automatic verification disabled for this workspace.")}
			if summary := strings.TrimSpace(report.FailureSummary()); summary != "" {
				lines = append(lines, rt.ui.warnLine("Latest verification failure:"), rt.ui.dim(summary))
			}
			rt.writeOutputLines(lines...)
			return AutoVerifyFailureDisable, nil
		case "", "n", "no":
			return AutoVerifyFailureNoAction, nil
		}
	}
}

func (rt *runtimeState) handleSetAnalysisModelsCommand(args string) error {
	parts := strings.Fields(args)
	if len(parts) > 0 {
		switch strings.ToLower(parts[0]) {
		case "status":
			return rt.showProjectAnalysisModelStatus()
		case "clear", "reset":
			rt.cfg.ProjectAnalysis.WorkerProfile = nil
			rt.cfg.ProjectAnalysis.ReviewerProfile = nil
			rt.rememberCurrentProfile()
			if err := rt.saveUserConfig(); err != nil {
				return err
			}
			fmt.Fprintln(rt.writer, rt.ui.successLine("Project analysis worker/reviewer models reset to the main active model."))
			return nil
		case "worker", "reviewer":
			provider := ""
			if len(parts) > 1 {
				provider = parts[1]
			}
			err := rt.configureProjectAnalysisRoleInteractive(strings.ToLower(parts[0]), provider)
			if errors.Is(err, ErrPromptCanceled) {
				return nil
			}
			return err
		default:
			return fmt.Errorf("usage: /set-analysis-models [status|clear|worker [provider]|reviewer [provider]]")
		}
	}

	fmt.Fprintln(rt.writer, rt.ui.section("Set Analysis Models"))
	if err := rt.showProjectAnalysisModelStatus(); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.info("  1. worker"))
	fmt.Fprintln(rt.writer, rt.ui.info("  2. reviewer"))
	choice, err := rt.promptValue("Select analysis role", "1")
	if errors.Is(err, ErrPromptCanceled) {
		return nil
	}
	if err != nil {
		return err
	}
	role := ""
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "", "1", "worker":
		role = "worker"
	case "2", "reviewer", "reviewr":
		role = "reviewer"
	default:
		return fmt.Errorf("unknown analysis role: %s", choice)
	}
	err = rt.configureProjectAnalysisRoleInteractive(role, "")
	if errors.Is(err, ErrPromptCanceled) {
		return nil
	}
	return err
}

func (rt *runtimeState) handleSetSpecialistModelCommand(args string) error {
	parts := strings.Fields(args)
	if len(parts) > 0 {
		switch strings.ToLower(strings.TrimSpace(parts[0])) {
		case "status":
			name := ""
			if len(parts) > 1 {
				name = parts[1]
			}
			return rt.showSpecialistModelStatus(name)
		case "clear", "reset":
			if len(parts) < 2 {
				return fmt.Errorf("usage: /set-specialist-model clear <specialist|all>")
			}
			return rt.clearSpecialistModelOverride(parts[1])
		default:
			name := parts[0]
			provider := ""
			if len(parts) > 1 {
				provider = parts[1]
			}
			model := ""
			if len(parts) > 2 {
				model = strings.Join(parts[2:], " ")
			}
			err := rt.configureSpecialistModelInteractive(name, provider, model)
			if errors.Is(err, ErrPromptCanceled) {
				return nil
			}
			return err
		}
	}

	fmt.Fprintln(rt.writer, rt.ui.section("Set Specialist Model"))
	if err := rt.showSpecialistModelStatus(""); err != nil {
		return err
	}
	names := rt.allSpecialistNames()
	if len(names) == 0 {
		return fmt.Errorf("no specialist profiles are configured")
	}
	for idx, name := range names {
		fmt.Fprintln(rt.writer, rt.ui.info(fmt.Sprintf("  %d. %s", idx+1, name)))
	}
	choice, err := rt.promptValue("Select specialist", "1")
	if errors.Is(err, ErrPromptCanceled) {
		return nil
	}
	if err != nil {
		return err
	}
	selected, err := rt.resolveSpecialistChoice(choice, names)
	if err != nil {
		return err
	}
	err = rt.configureSpecialistModelInteractive(selected, "", "")
	if errors.Is(err, ErrPromptCanceled) {
		return nil
	}
	return err
}

func parsePlanItemsFromText(plan string) []PlanItem {
	plan = strings.ReplaceAll(plan, "\r\n", "\n")
	plan = strings.TrimSpace(plan)
	if plan == "" {
		return nil
	}

	lines := strings.Split(plan, "\n")
	items := make([]PlanItem, 0, len(lines))
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if match := numberedPlanItemPattern.FindStringSubmatch(line); len(match) == 2 {
			items = append(items, PlanItem{
				Step:   strings.TrimSpace(match[1]),
				Status: "pending",
			})
			continue
		}
		if match := bulletPlanItemPattern.FindStringSubmatch(line); len(match) == 2 {
			items = append(items, PlanItem{
				Step:   strings.TrimSpace(match[1]),
				Status: "pending",
			})
		}
	}
	if len(items) > 0 {
		return items
	}
	return []PlanItem{{
		Step:   compactPersistentMemoryText(plan, 220),
		Status: "pending",
	}}
}

func (rt *runtimeState) seedSessionPlanFromText(plan string) error {
	if rt == nil || rt.session == nil || rt.store == nil {
		return nil
	}
	items := parsePlanItemsFromText(plan)
	if len(items) == 0 {
		return nil
	}
	rt.session.SetSharedPlan(items)
	return rt.store.Save(rt.session)
}

func (rt *runtimeState) showProjectAnalysisModelStatus() error {
	fmt.Fprintln(rt.writer, rt.ui.section("Project Analysis Models"))
	if rt.cfg.ProjectAnalysis.WorkerProfile == nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("worker", inheritedMainModelLabel(rt.cfg.Provider, rt.cfg.Model, rt.cfg.ReasoningEffort)))
	} else {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("worker", formatProviderModelEffortLabel(rt.cfg.ProjectAnalysis.WorkerProfile.Provider, rt.cfg.ProjectAnalysis.WorkerProfile.Model, rt.cfg.ProjectAnalysis.WorkerProfile.ReasoningEffort)))
	}
	if rt.cfg.ProjectAnalysis.ReviewerProfile == nil {
		if rt.cfg.ProjectAnalysis.WorkerProfile != nil {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("reviewer", inheritedWorkerModelLabel(rt.cfg.ProjectAnalysis.WorkerProfile.Provider, rt.cfg.ProjectAnalysis.WorkerProfile.Model, rt.cfg.ProjectAnalysis.WorkerProfile.ReasoningEffort)))
		} else {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("reviewer", inheritedMainModelLabel(rt.cfg.Provider, rt.cfg.Model, rt.cfg.ReasoningEffort)))
		}
	} else {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("reviewer", formatProviderModelEffortLabel(rt.cfg.ProjectAnalysis.ReviewerProfile.Provider, rt.cfg.ProjectAnalysis.ReviewerProfile.Model, rt.cfg.ProjectAnalysis.ReviewerProfile.ReasoningEffort)))
	}
	incrementalEnabled := rt.cfg.ProjectAnalysis.Incremental == nil || *rt.cfg.ProjectAnalysis.Incremental
	fmt.Fprintln(rt.writer, rt.ui.statusKV("incremental", fmt.Sprintf("%t", incrementalEnabled)))
	return nil
}

func (rt *runtimeState) showSpecialistModelStatus(name string) error {
	fmt.Fprintln(rt.writer, rt.ui.section("Specialist Models"))
	target := normalizeSpecialistProfileName(name)
	selected := make([]SpecialistSubagentProfile, 0, len(configuredSpecialistProfiles(rt.cfg)))
	for _, profile := range configuredSpecialistProfiles(rt.cfg) {
		if target != "" && normalizeSpecialistProfileName(profile.Name) != target {
			continue
		}
		selected = append(selected, profile)
	}
	if target != "" && len(selected) == 0 {
		return fmt.Errorf("unknown specialist profile: %s", name)
	}
	columnWidth := specialistStatusColumnWidth(selected)
	for _, profile := range selected {
		fmt.Fprintln(rt.writer, rt.ui.statusKVAligned(profile.Name, rt.describeSpecialistModel(profile), columnWidth))
	}
	return nil
}

func (rt *runtimeState) describeSpecialistModel(profile SpecialistSubagentProfile) string {
	cfg := Config{}
	if rt != nil {
		cfg = rt.cfg
	}
	provider := firstNonBlankString(profile.Provider, cfg.Provider)
	if provider == "" && rt != nil && rt.session != nil {
		provider = strings.TrimSpace(rt.session.Provider)
	}
	model := firstNonBlankString(profile.Model, cfg.Model)
	if model == "" && rt != nil && rt.session != nil {
		model = strings.TrimSpace(rt.session.Model)
	}
	if provider == "" || model == "" {
		return "interactive reviewer fallback"
	}
	effort := profile.ReasoningEffort
	if strings.TrimSpace(profile.Provider) == "" && strings.TrimSpace(profile.Model) == "" {
		effort = cfg.ReasoningEffort
	}
	value := formatProviderModelEffortLabel(provider, model, effort)
	if strings.TrimSpace(profile.Provider) == "" && strings.TrimSpace(profile.Model) == "" {
		value = inheritedMainModelLabel(provider, model, effort)
	}
	return value
}

func (rt *runtimeState) configureSpecialistModelInteractive(name string, providerArg string, modelArg string) error {
	profile, ok := configuredSpecialistProfileByName(rt.cfg, name)
	if !ok {
		return fmt.Errorf("unknown specialist profile: %s", name)
	}
	provider := normalizeProviderName(providerArg)
	if provider == "" {
		fmt.Fprintln(rt.writer, rt.ui.section("Set Specialist "+profile.Name))
		rt.printProviderChoiceOptions()
		defaultChoice := defaultProviderChoice(firstNonBlankString(profile.Provider, rt.cfg.Provider))
		choice, err := rt.promptValue("Select provider", defaultChoice)
		if err != nil {
			return err
		}
		if strings.TrimSpace(choice) == "" {
			choice = defaultChoice
		}
		resolved, ok := resolveProviderChoice(choice)
		if !ok {
			return fmt.Errorf("unknown provider: %s", choice)
		}
		provider = resolved
	}

	nextModel := strings.TrimSpace(modelArg)
	nextBaseURL := ""
	nextAPIKey := ""
	currentModel := ""
	if strings.EqualFold(strings.TrimSpace(profile.Provider), provider) {
		nextBaseURL = strings.TrimSpace(profile.BaseURL)
		nextAPIKey = strings.TrimSpace(profile.APIKey)
		currentModel = strings.TrimSpace(profile.Model)
	}
	if strings.TrimSpace(nextAPIKey) == "" {
		nextAPIKey = rt.providerAPIKey(provider)
	}

	if nextModel == "" {
		switch provider {
		case "ollama":
			defaultURL := nextBaseURL
			if strings.TrimSpace(defaultURL) == "" {
				defaultURL = normalizeOllamaBaseURL("")
			}
			url, err := rt.promptValue("Ollama URL", defaultURL)
			if err != nil {
				return err
			}
			url = normalizeOllamaBaseURL(url)
			models, normalized, fetchErr := rt.fetchAndShowOllamaModels(url)
			if fetchErr != nil {
				return fmt.Errorf("could not connect to Ollama server: %w", fetchErr)
			}
			if len(models) == 0 {
				return fmt.Errorf("no Ollama models were returned by %s", normalized)
			}
			rt.ollamaModels = models
			selected, err := rt.chooseOllamaModel(models)
			if err != nil {
				return err
			}
			nextModel = selected.Name
			nextBaseURL = normalized
		case "openrouter":
			nextBaseURL = normalizeOpenRouterBaseURL(nextBaseURL)
			if strings.TrimSpace(nextAPIKey) == "" {
				apiKey, err := rt.promptRequiredValue(providerDisplayName(provider)+" API key (for specialist "+profile.Name+")", "")
				if err != nil {
					return err
				}
				nextAPIKey = apiKey
			}
			models, normalized, err := rt.fetchAndShowOpenRouterModels(nextBaseURL, nextAPIKey)
			if err != nil {
				return err
			}
			selected, err := rt.chooseOpenRouterModel(models)
			if err != nil {
				return err
			}
			nextModel = selected.ID
			nextBaseURL = normalized
		case "deepseek":
			nextBaseURL = normalizeDeepSeekBaseURL(nextBaseURL)
			if strings.TrimSpace(nextAPIKey) == "" {
				apiKey, err := rt.promptRequiredValue(providerDisplayName(provider)+" API key (for specialist "+profile.Name+")", "")
				if err != nil {
					return err
				}
				nextAPIKey = apiKey
			}
			model, normalized, err := rt.fetchAndChooseDeepSeekModel(nextBaseURL, nextAPIKey, currentModel)
			if err != nil {
				return err
			}
			nextModel = model
			nextBaseURL = normalized
		case "anthropic":
			model, err := rt.chooseAnthropicModel(currentModel)
			if err != nil {
				return err
			}
			nextModel = model
		case "openai":
			model, err := rt.chooseOpenAIModel(currentModel)
			if err != nil {
				return err
			}
			nextModel = model
		case "opencode", "opencode-go":
			nextBaseURL = normalizeOpenCodeProviderBaseURL(provider, nextBaseURL)
			if strings.TrimSpace(nextAPIKey) == "" {
				apiKey, err := rt.promptRequiredValue(providerDisplayName(provider)+" API key (for specialist "+profile.Name+")", "")
				if err != nil {
					return err
				}
				nextAPIKey = apiKey
			}
			models, normalized, err := rt.fetchAndShowOpenCodeModelsForProvider(provider, nextBaseURL, nextAPIKey)
			if err != nil {
				return err
			}
			nextModel, err = rt.chooseOpenCodeModelForProvider(provider, models, currentModel)
			if err != nil {
				return err
			}
			nextBaseURL = normalized
		case "codex-cli":
			model, err := rt.chooseCodexCLIModel(currentModel)
			if err != nil {
				return err
			}
			nextModel = model
		case "anthropic-claude-cli":
			if err := rt.configureClaudeCLICommandInteractive(); err != nil {
				return err
			}
			model, err := rt.chooseClaudeCLIModel(currentModel)
			if err != nil {
				return err
			}
			nextModel = model
		case "openai-codex":
			model, err := rt.chooseOpenAICodexModel(currentModel)
			if err != nil {
				return err
			}
			nextModel = model
			nextBaseURL = normalizeOpenAICodexBaseURL(nextBaseURL)
			nextAPIKey = ""
		case "lmstudio", "vllm", "llama.cpp":
			model, normalized, apiKey, err := rt.configureLocalOpenAICompatibleModel(provider, currentModel, nextBaseURL, nextAPIKey, "specialist "+profile.Name)
			if err != nil {
				return err
			}
			nextModel = model
			nextBaseURL = normalized
			nextAPIKey = apiKey
		default:
			return fmt.Errorf("unsupported provider: %s", provider)
		}
	} else {
		switch provider {
		case "ollama":
			nextBaseURL = normalizeOllamaBaseURL(nextBaseURL)
		case "openrouter":
			nextBaseURL = normalizeOpenRouterBaseURL(nextBaseURL)
		case "opencode", "opencode-go":
			nextBaseURL = normalizeOpenCodeProviderBaseURL(provider, nextBaseURL)
		case "deepseek":
			nextBaseURL = normalizeDeepSeekBaseURL(nextBaseURL)
		case "anthropic", "openai", "codex-cli", "anthropic-claude-cli", "openai-codex", "lmstudio", "vllm", "llama.cpp":
			nextBaseURL = normalizeProfileBaseURL(provider, nextBaseURL)
		default:
			return fmt.Errorf("unsupported provider: %s", provider)
		}
	}

	return rt.activateSpecialistModel(profile.Name, provider, nextModel, nextBaseURL, nextAPIKey)
}

func (rt *runtimeState) configureProjectAnalysisRoleInteractive(role string, providerArg string) error {
	provider := normalizeProviderName(providerArg)
	if provider == "" {
		fmt.Fprintln(rt.writer, rt.ui.section("Set Analysis "+strings.Title(role)))
		rt.printProviderChoiceOptions()
		defaultChoice := defaultProviderChoice("")
		current := rt.cfg.ProjectAnalysis.WorkerProfile
		if role == "reviewer" {
			current = rt.cfg.ProjectAnalysis.ReviewerProfile
		}
		if current != nil {
			defaultChoice = defaultProviderChoice(current.Provider)
		}
		choice, err := rt.promptValue("Select provider", defaultChoice)
		if err != nil {
			return err
		}
		if strings.TrimSpace(choice) == "" {
			choice = defaultChoice
		}
		resolved, ok := resolveProviderChoice(choice)
		if !ok {
			return fmt.Errorf("unknown provider: %s", choice)
		}
		provider = resolved
	}

	nextModel := ""
	nextBaseURL := ""
	nextAPIKey := ""
	current := rt.cfg.ProjectAnalysis.WorkerProfile
	if role == "reviewer" {
		current = rt.cfg.ProjectAnalysis.ReviewerProfile
	}
	if current != nil && strings.EqualFold(current.Provider, provider) {
		nextModel = current.Model
		nextBaseURL = current.BaseURL
		nextAPIKey = current.APIKey
	}
	if strings.TrimSpace(nextAPIKey) == "" {
		nextAPIKey = rt.providerAPIKey(provider)
	}

	switch provider {
	case "ollama":
		defaultURL := nextBaseURL
		if strings.TrimSpace(defaultURL) == "" {
			defaultURL = normalizeOllamaBaseURL("")
		}
		url, err := rt.promptValue("Ollama URL", defaultURL)
		if err != nil {
			return err
		}
		url = normalizeOllamaBaseURL(url)
		models, normalized, fetchErr := rt.fetchAndShowOllamaModels(url)
		if fetchErr != nil {
			return fmt.Errorf("could not connect to Ollama server: %w", fetchErr)
		}
		if len(models) == 0 {
			return fmt.Errorf("no Ollama models were returned by %s", normalized)
		}
		rt.ollamaModels = models
		selected, err := rt.chooseOllamaModel(models)
		if err != nil {
			return err
		}
		nextModel = selected.Name
		nextBaseURL = normalized
	case "lmstudio", "vllm", "llama.cpp":
		model, normalized, apiKey, err := rt.configureLocalOpenAICompatibleModel(provider, nextModel, nextBaseURL, nextAPIKey, "analysis "+role)
		if err != nil {
			return err
		}
		nextModel = model
		nextBaseURL = normalized
		nextAPIKey = apiKey
	case "anthropic", "openai", "openrouter", "deepseek", "opencode", "opencode-go":
		if strings.TrimSpace(nextAPIKey) == "" {
			keyPrompt := providerDisplayName(provider) + " API key (for analysis " + role + ")"
			apiKey, err := rt.promptRequiredValue(keyPrompt, "")
			if err != nil {
				return err
			}
			nextAPIKey = apiKey
		}
		if provider == "openrouter" {
			nextBaseURL = normalizeOpenRouterBaseURL("")
			models, normalized, err := rt.fetchAndShowOpenRouterModels(nextBaseURL, nextAPIKey)
			if err != nil {
				return err
			}
			selected, err := rt.chooseOpenRouterModel(models)
			if err != nil {
				return err
			}
			nextModel = selected.ID
			nextBaseURL = normalized
		} else if provider == "deepseek" {
			nextBaseURL = normalizeDeepSeekBaseURL(nextBaseURL)
			model, normalized, err := rt.fetchAndChooseDeepSeekModel(nextBaseURL, nextAPIKey, nextModel)
			if err != nil {
				return err
			}
			nextModel = model
			nextBaseURL = normalized
		} else if isOpenCodeProvider(provider) {
			nextBaseURL = normalizeOpenCodeProviderBaseURL(provider, nextBaseURL)
			models, normalized, err := rt.fetchAndShowOpenCodeModelsForProvider(provider, nextBaseURL, nextAPIKey)
			if err != nil {
				return err
			}
			nextModel, err = rt.chooseOpenCodeModelForProvider(provider, models, nextModel)
			if err != nil {
				return err
			}
			nextBaseURL = normalized
		} else if provider == "anthropic" {
			model, err := rt.chooseAnthropicModel(nextModel)
			if err != nil {
				return err
			}
			nextModel = model
		} else {
			model, err := rt.chooseOpenAIModel(nextModel)
			if err != nil {
				return err
			}
			nextModel = model
		}
	case "openai-codex":
		model, err := rt.chooseOpenAICodexModel(nextModel)
		if err != nil {
			return err
		}
		nextModel = model
		nextBaseURL = normalizeOpenAICodexBaseURL(nextBaseURL)
		nextAPIKey = ""
	case "codex-cli":
		model, err := rt.chooseCodexCLIModel(nextModel)
		if err != nil {
			return err
		}
		nextModel = model
		nextBaseURL = ""
		nextAPIKey = ""
	case "anthropic-claude-cli":
		if err := rt.configureClaudeCLICommandInteractive(); err != nil {
			return err
		}
		model, err := rt.chooseClaudeCLIModel(nextModel)
		if err != nil {
			return err
		}
		nextModel = model
		nextBaseURL = ""
		nextAPIKey = ""
	default:
		return fmt.Errorf("unsupported provider: %s", provider)
	}

	return rt.activateProjectAnalysisRole(role, provider, nextModel, nextBaseURL, nextAPIKey)
}

func (rt *runtimeState) activateProjectAnalysisRole(role string, provider string, model string, baseURL string, apiKey string) error {
	provider = normalizeProviderName(provider)
	current := rt.cfg.ProjectAnalysis.WorkerProfile
	if role == "reviewer" {
		current = rt.cfg.ProjectAnalysis.ReviewerProfile
	}
	if strings.TrimSpace(apiKey) == "" {
		apiKey = rt.providerAPIKey(provider)
	}
	if isOpenCodeProvider(provider) {
		resolvedModel, resolvedBaseURL, err := rt.resolveOpenCodeModelForProviderAPIKey(provider, model, baseURL, apiKey, "analysis "+role)
		if err != nil {
			return err
		}
		model = resolvedModel
		baseURL = resolvedBaseURL
	}
	profile := &Profile{
		Name:     profileName(provider, model),
		Provider: provider,
		Model:    model,
		BaseURL:  normalizeProfileBaseURL(provider, baseURL),
		APIKey:   apiKey,
	}
	nextEffort, defaultedEffort := rt.profileReasoningEffortForActivation(current, provider, model, baseURL)
	profile.ReasoningEffort = nextEffort
	if role == "worker" {
		rt.cfg.ProjectAnalysis.WorkerProfile = profile
	} else if role == "reviewer" {
		rt.cfg.ProjectAnalysis.ReviewerProfile = profile
	} else {
		return fmt.Errorf("unsupported analysis role: %s", role)
	}
	rt.storeProviderKey(provider, apiKey)
	rt.rememberCurrentProfile()
	if err := rt.saveUserConfig(); err != nil {
		return err
	}
	rt.printReasoningEffortDefaultNotice("analysis "+role+" model", defaultedEffort)
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Analysis %s set: %s", role, formatProviderModelLabel(provider, model))))
	return nil
}

func (rt *runtimeState) activateSpecialistModel(name string, provider string, model string, baseURL string, apiKey string) error {
	provider = normalizeProviderName(provider)
	if strings.TrimSpace(apiKey) == "" {
		apiKey = rt.providerAPIKey(provider)
	}
	if isOpenCodeProvider(provider) {
		resolvedModel, resolvedBaseURL, err := rt.resolveOpenCodeModelForProviderAPIKey(provider, model, baseURL, apiKey, "specialist "+name)
		if err != nil {
			return err
		}
		model = resolvedModel
		baseURL = resolvedBaseURL
	}
	nextEffort, defaultedEffort := rt.specialistReasoningEffortForActivation(name, provider, model, baseURL)
	updated := false
	profiles := append([]SpecialistSubagentProfile(nil), rt.cfg.Specialists.Profiles...)
	for i := range profiles {
		if normalizeSpecialistProfileName(profiles[i].Name) != normalizeSpecialistProfileName(name) {
			continue
		}
		profiles[i].Provider = strings.TrimSpace(provider)
		profiles[i].Model = strings.TrimSpace(model)
		profiles[i].BaseURL = normalizeProfileBaseURL(provider, baseURL)
		profiles[i].APIKey = strings.TrimSpace(apiKey)
		profiles[i].ReasoningEffort = nextEffort
		updated = true
		break
	}
	if !updated {
		profiles = append(profiles, SpecialistSubagentProfile{
			Name:            strings.TrimSpace(name),
			Provider:        strings.TrimSpace(provider),
			Model:           strings.TrimSpace(model),
			BaseURL:         normalizeProfileBaseURL(provider, baseURL),
			APIKey:          strings.TrimSpace(apiKey),
			ReasoningEffort: nextEffort,
		})
	}
	rt.cfg.Specialists.Profiles = normalizeSpecialistProfiles(profiles)
	rt.storeProviderKey(provider, apiKey)
	rt.rememberCurrentProfile()
	if err := rt.persistSpecialistOverrides(); err != nil {
		return err
	}
	rt.printReasoningEffortDefaultNotice("specialist "+strings.TrimSpace(name)+" model", defaultedEffort)
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Specialist %s set: %s", name, formatProviderModelLabel(provider, model))))
	return nil
}

func (rt *runtimeState) handleTrustCommand(args string) error {
	action := strings.ToLower(strings.TrimSpace(args))
	switch action {
	case "", "status":
		rt.printProjectTrustStatus()
		return nil
	case "on", "trust", "trusted":
		return rt.setProjectTrustLevel("trusted")
	case "off", "untrust", "untrusted":
		return rt.setProjectTrustLevel("untrusted")
	default:
		return fmt.Errorf("usage: /trust [status|on|off]")
	}
}

func (rt *runtimeState) warnIfProjectLocalConfigIgnored() {
	if rt == nil || rt.writer == nil {
		return
	}
	if strings.TrimSpace(rt.workspace.BaseRoot) == "" || configProjectTrusted(rt.cfg, rt.workspace.BaseRoot) {
		return
	}
	fmt.Fprintln(rt.writer, rt.ui.warnLine("Project-local config saved but ignored until /trust on for this project."))
}

func (rt *runtimeState) setProjectTrustLevel(level string) error {
	key, err := SaveProjectTrustLevel(rt.workspace.BaseRoot, level)
	if err != nil {
		return err
	}
	if err := rt.reloadRuntimeConfig(); err != nil {
		return err
	}
	if strings.EqualFold(level, "trusted") {
		fmt.Fprintln(rt.writer, rt.ui.successLine("Project trusted. Project-local config and hooks are now eligible to load."))
	} else {
		fmt.Fprintln(rt.writer, rt.ui.successLine("Project marked untrusted. Project-local config and hooks will be ignored."))
	}
	fmt.Fprintln(rt.writer, rt.ui.statusKV("trust_key", key))
	return nil
}

func (rt *runtimeState) printProjectTrustStatus() {
	level := normalizeProjectTrustLevel(projectTrustLevelForPath(rt.cfg, rt.workspace.BaseRoot))
	if level == "" {
		level = "unknown"
	}
	keys := projectTrustCandidateKeys(rt.workspace.BaseRoot)
	key := ""
	if len(keys) > 0 {
		key = keys[0]
	}
	localConfig := "ignored"
	localHooks := "ignored"
	if strings.EqualFold(level, "trusted") {
		localConfig = "eligible"
		localHooks = "eligible"
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Project Trust"))
	rt.printKVGroup("Status",
		kv("workspace", valueOrUnset(rt.workspace.BaseRoot)),
		kv("trust_level", level),
		kv("trust_key", valueOrUnset(key)),
		kv("project_config", localConfig),
		kv("project_hooks", localHooks),
	)
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Use /trust on to allow this project to load .kernforge/config.json and .kernforge/hooks.json. Use /trust off to disable them again."))
}

func (rt *runtimeState) clearSpecialistModelOverride(name string) error {
	target := normalizeSpecialistProfileName(name)
	if target == "" {
		return fmt.Errorf("usage: /set-specialist-model clear <specialist|all>")
	}
	removed := false
	current := append([]SpecialistSubagentProfile(nil), rt.cfg.Specialists.Profiles...)
	next := make([]SpecialistSubagentProfile, 0, len(current))
	for _, profile := range current {
		match := target == "all" || normalizeSpecialistProfileName(profile.Name) == target
		if !match {
			next = append(next, profile)
			continue
		}
		removed = true
		profile.Provider = ""
		profile.Model = ""
		profile.BaseURL = ""
		profile.APIKey = ""
		profile.ReasoningEffort = ""
		if specialistProfileHasNonModelOverrides(profile) {
			next = append(next, normalizeSpecialistProfile(profile))
		}
	}
	if !removed {
		if target == "all" {
			fmt.Fprintln(rt.writer, rt.ui.warnLine("No specialist model overrides were set."))
			return nil
		}
		return fmt.Errorf("no specialist model override found for %s", name)
	}
	rt.cfg.Specialists.Profiles = normalizeSpecialistProfiles(next)
	rt.rememberCurrentProfile()
	if err := rt.persistSpecialistOverrides(); err != nil {
		return err
	}
	if target == "all" {
		fmt.Fprintln(rt.writer, rt.ui.successLine("Cleared all specialist model overrides."))
	} else {
		fmt.Fprintln(rt.writer, rt.ui.successLine("Cleared specialist model override for "+name+"."))
	}
	return nil
}

func specialistProfileHasNonModelOverrides(profile SpecialistSubagentProfile) bool {
	return strings.TrimSpace(profile.Description) != "" ||
		strings.TrimSpace(profile.Prompt) != "" ||
		len(profile.NodeKinds) > 0 ||
		len(profile.Keywords) > 0 ||
		profile.ReadOnly != nil ||
		profile.Editable != nil ||
		len(profile.OwnershipPaths) > 0
}

func (rt *runtimeState) persistSpecialistOverrides() error {
	if rt == nil {
		return nil
	}
	if rt.agent != nil {
		rt.agent.Config = rt.cfg
	}
	if strings.TrimSpace(rt.workspace.BaseRoot) == "" {
		return rt.saveUserConfig()
	}
	overrides := map[string]any{}
	if !specialistConfigIsEmpty(rt.cfg.Specialists) {
		overrides["specialists"] = rt.cfg.Specialists
	} else {
		overrides["specialists"] = nil
	}
	if err := SaveWorkspaceConfigOverrides(rt.workspace.BaseRoot, overrides); err != nil {
		return err
	}
	rt.warnIfProjectLocalConfigIgnored()
	return nil
}

func specialistConfigIsEmpty(cfg SpecialistSubagentsConfig) bool {
	enabledDefault := cfg.Enabled == nil
	if cfg.Enabled != nil && *cfg.Enabled {
		enabledDefault = true
	}
	return enabledDefault && len(cfg.Profiles) == 0
}

func (rt *runtimeState) resolveSpecialistChoice(choice string, names []string) (string, error) {
	trimmed := strings.TrimSpace(choice)
	if trimmed == "" {
		if len(names) == 0 {
			return "", fmt.Errorf("no specialist profiles are configured")
		}
		return names[0], nil
	}
	for idx, name := range names {
		if trimmed == fmt.Sprintf("%d", idx+1) || strings.EqualFold(trimmed, name) {
			return name, nil
		}
	}
	return "", fmt.Errorf("unknown specialist profile: %s", choice)
}

func (rt *runtimeState) handleAnalyzeProjectCommand(args string) error {
	parsed, err := parseAnalyzeProjectCommandArgs(args)
	if err != nil {
		return err
	}
	mode := parsed.Mode
	goal := parsed.Goal
	if rt.agent == nil || rt.agent.Client == nil {
		return fmt.Errorf("no model provider is configured")
	}

	fmt.Fprintln(rt.writer, rt.ui.section("Project Analysis"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("planner", rt.session.Provider+" / "+rt.session.Model))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("conductor_model", rt.session.Provider+" / "+rt.session.Model))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("workspace", rt.session.WorkingDir))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("runtime_error_log", runtimeErrorLogPath(rt.workspace.BaseRoot)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("analysis_mode", projectAnalysisModeStatus(mode, goal)))
	analysisCfg := configProjectAnalysis(rt.cfg, rt.workspace.BaseRoot)
	analysisWorkspace, analysisPaths, err := prepareExplicitAnalysisWorkspace(rt.workspace, parsed.Paths)
	if err != nil {
		return err
	}
	analysisCfg, err = rt.prepareAnalysisDirectorySelection(analysisWorkspace.Root, analysisCfg)
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(analysisWorkspace.Root), strings.TrimSpace(rt.workspace.Root)) {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("analysis_root", analysisWorkspace.Root))
	}
	workerLabel := rt.session.Provider + " / " + rt.session.Model
	reviewerLabel := workerLabel
	if analysisCfg.WorkerProfile != nil {
		workerLabel = analysisCfg.WorkerProfile.Provider + " / " + analysisCfg.WorkerProfile.Model
	}
	if analysisCfg.ReviewerProfile != nil {
		reviewerLabel = analysisCfg.ReviewerProfile.Provider + " / " + analysisCfg.ReviewerProfile.Model
	} else if analysisCfg.WorkerProfile != nil {
		reviewerLabel = workerLabel
	}
	incrementalEnabled := analysisCfg.Incremental == nil || *analysisCfg.Incremental
	effectiveMode := effectiveProjectAnalysisMode(mode, goal)
	fmt.Fprintln(rt.writer, rt.ui.statusKV("analysis_worker", workerLabel))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("analysis_reviewer", reviewerLabel))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("incremental", fmt.Sprintf("%t", incrementalEnabled)))

	analyzer := newProjectAnalyzer(rt.cfg, rt.agent.Client, analysisWorkspace, func(status string) {
		fmt.Fprintln(rt.writer, rt.ui.hintLine(status))
	}, func(debug string) {
		fmt.Fprintln(rt.writer, rt.ui.infoLine("analysis: "+debug))
	})
	analyzer.analysisCfg = analysisCfg
	previewSnapshot, err := analyzer.scanProject()
	if err != nil {
		return err
	}
	for _, note := range analyzer.applyAdaptiveAnalysisShardSizing(previewSnapshot) {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("adaptive_shard_sizing", note))
	}
	analysisCfg = analyzer.analysisCfg
	estimatedConcurrency := analyzer.estimateAgentCount(previewSnapshot)
	estimatedTotalShards := analyzer.estimateShardCount(previewSnapshot, estimatedConcurrency)
	plannedShards := analyzer.planShards(previewSnapshot, estimatedTotalShards)
	if len(plannedShards) > 0 {
		estimatedTotalShards = len(plannedShards)
	}
	explicitScope, unmatchedPaths := resolveExplicitAnalysisScope(analysisPaths, previewSnapshot)
	if len(unmatchedPaths) > 0 {
		return fmt.Errorf("analysis path not found in scanned workspace: %s", strings.Join(unmatchedPaths, ", "))
	}
	scope := deriveAnalysisScope(goal, explicitScope, previewSnapshot)
	_, scopedShards := deriveScopedAnalysisShardsForScope(scope, plannedShards)
	if len(scopedShards) > 0 {
		plannedShards = scopedShards
		estimatedTotalShards = len(plannedShards)
		if estimatedConcurrency > estimatedTotalShards {
			estimatedConcurrency = estimatedTotalShards
		}
		if estimatedConcurrency < 1 {
			estimatedConcurrency = 1
		}
		fmt.Fprintln(rt.writer, rt.ui.statusKV("analysis_scope", strings.Join(scope.DirectoryPrefixes, ", ")))
	} else if len(scope.DirectoryPrefixes) > 0 {
		return fmt.Errorf("analysis scope matched no analyzable shards: %s", strings.Join(scope.DirectoryPrefixes, ", "))
	} else if analysisGoalHasDirectoryHint(strings.ToLower(filepath.ToSlash(goal))) {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No matching analysis scope was found from the prompt; the plan will cover the scanned workspace."))
	}
	estimatedConcurrency = analyzer.effectiveShardConcurrency(estimatedConcurrency, len(plannedShards), effectiveMode)
	if normalizeProjectAnalysisMode(effectiveMode) != "" && normalizeProjectAnalysisMode(effectiveMode) != "map" {
		if baseline, ok := analyzer.loadBaselineMapForMode(effectiveMode, scope); ok {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("baseline_map", baseline.RunID))
			if strings.TrimSpace(baseline.Goal) != "" {
				fmt.Fprintln(rt.writer, rt.ui.statusKV("baseline_map_goal", baseline.Goal))
			}
			if strings.TrimSpace(baseline.ArtifactPath) != "" {
				fmt.Fprintln(rt.writer, rt.ui.statusKV("baseline_map_artifact", baseline.ArtifactPath))
			}
			if len(baseline.SourceAnchors) > 0 {
				fmt.Fprintln(rt.writer, rt.ui.statusKV("baseline_map_anchors", fmt.Sprintf("%d", len(baseline.SourceAnchors))))
			}
		} else {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("baseline_map", "none"))
			fmt.Fprintln(rt.writer, rt.ui.hintLine("Run /analyze-project --mode map first to give follow-up modes a reusable architecture baseline."))
		}
	}
	estimatedWaves := ceilDiv(estimatedTotalShards, analysisMaxInt(estimatedConcurrency, 1))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("estimated_files", fmt.Sprintf("%d", previewSnapshot.TotalFiles)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("estimated_lines", fmt.Sprintf("%d", previewSnapshot.TotalLines)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("estimated_dirs", fmt.Sprintf("%d", len(previewSnapshot.Directories))))
	if strings.TrimSpace(previewSnapshot.PrimaryStartup) != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("estimated_startup", previewSnapshot.PrimaryStartup))
	}
	fmt.Fprintln(rt.writer, rt.ui.statusKV("estimated_concurrency", fmt.Sprintf("%d", estimatedConcurrency)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("estimated_total_shards", fmt.Sprintf("%d", estimatedTotalShards)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("estimated_waves", fmt.Sprintf("%d", estimatedWaves)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("max_files_per_shard", fmt.Sprintf("%d", analysisCfg.MaxFilesPerShard)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("max_lines_per_shard", fmt.Sprintf("%d", analysisCfg.MaxLinesPerShard)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("max_total_shards", fmt.Sprintf("%d", analysisCfg.MaxTotalShards)))
	fmt.Fprintln(rt.writer)

	if rt.interactive {
		proceed, err := rt.confirm("Proceed with this analysis plan?")
		if err != nil {
			return err
		}
		if !proceed {
			fmt.Fprintln(rt.writer, rt.ui.warnLine("Project analysis aborted by user."))
			return nil
		}
	} else {
		fmt.Fprintln(rt.writer, rt.ui.hintLine("Interactive confirmation unavailable; proceeding with the displayed analysis plan."))
		fmt.Fprintln(rt.writer)
	}

	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stopEscapeWatcher := startEscapeWatcher(cancel, rt.shouldHonorRequestCancel, rt.confirmRequestCancel)
	defer stopEscapeWatcher()

	rt.startThinkingIndicator()
	defer rt.stopThinkingIndicator()
	analysisStatus := func(status string) {
		if requestCtx.Err() != nil {
			return
		}
		rt.setThinkingStatus(compactThinkingStatus(rt.cfg, status))
		line := rt.ui.activityLine("analysis", status)
		if configProgressDisplay(rt.cfg) == "compact" {
			if !rt.showTransientWhileThinking(line) {
				rt.printPersistentWhileThinking(line)
			}
			return
		}
		rt.printPersistentWhileThinking(line)
	}
	analysisDebug := func(debug string) {
		if requestCtx.Err() != nil {
			return
		}
		rt.setThinkingStatus(compactThinkingStatus(rt.cfg, debug))
		line := rt.ui.activityLine("analysis", debug)
		if configProgressDisplay(rt.cfg) == "stream" {
			rt.printPersistentWhileThinking(line)
			return
		}
		if !rt.showTransientWhileThinking(line) {
			rt.printPersistentWhileThinking(line)
		}
	}
	analysisProgress := func(event ProgressEvent) {
		if requestCtx.Err() != nil {
			return
		}
		rt.printProgressEvent(event)
	}

	analyzer = newProjectAnalyzer(rt.cfg, rt.agent.Client, analysisWorkspace, analysisStatus, analysisDebug)
	analyzer.analysisCfg = analysisCfg
	analyzer.explicitScope = explicitScope
	analyzer.onProgress = analysisProgress
	run, err := analyzer.Run(requestCtx, goal, mode)
	if err != nil {
		if requestCtx.Err() == context.Canceled {
			rt.noteRecentRequestCancel()
			return fmt.Errorf("project analysis canceled by user")
		}
		if analysisShouldRetryWithSmallerShards(err) {
			initialErr := err
			recoveryCfg, recoveryNote, ok := analysisProviderFailureRecoveryConfig(analyzer.analysisCfg, previewSnapshot)
			if ok {
				rt.printPersistentWhileThinking(rt.ui.warnLine("Project analysis hit a model/provider timeout or transient error; retrying once with smaller shards."))
				rt.printPersistentWhileThinking(rt.ui.statusKV("adaptive_retry_shards", recoveryNote))
				analyzer = newProjectAnalyzer(rt.cfg, rt.agent.Client, analysisWorkspace, analysisStatus, analysisDebug)
				analyzer.analysisCfg = recoveryCfg
				analyzer.explicitScope = explicitScope
				analyzer.onProgress = analysisProgress
				run, err = analyzer.Run(requestCtx, goal, mode)
				if err == nil {
					analysisCfg = recoveryCfg
				}
				if err != nil {
					if requestCtx.Err() == context.Canceled {
						rt.noteRecentRequestCancel()
						return fmt.Errorf("project analysis canceled by user")
					}
					return fmt.Errorf("project analysis failed after automatic smaller-shard retry: initial error: %v; retry error: %w", initialErr, err)
				}
			}
		}
		if err != nil {
			return err
		}
	}
	rt.clearThinkingStatus()
	rt.clearThinkingDetails()

	rt.printPersistentWhileThinking(rt.ui.activityLine("analysis", "Saving analysis session metadata..."))
	rt.session.LastAnalysis = &run.Summary
	rt.session.Summary = mergeSessionSummaryWithAnalysis(rt.session.Summary, run)
	rt.session.LastAnalysisContextQuery = ""
	rt.session.LastAnalysisContextRunID = run.Summary.RunID
	if err := rt.store.Save(rt.session); err != nil {
		return err
	}

	rt.printPersistentWhileThinking(rt.ui.successLine(fmt.Sprintf("Analysis completed with %d shard(s).", run.Summary.TotalShards)))
	if run.Summary.ReviewFailures > 0 {
		rt.printPersistentWhileThinking(rt.ui.statusKV("review_failures", fmt.Sprintf("%d", run.Summary.ReviewFailures)))
	}
	if run.Summary.ReviewProviderFailures > 0 {
		rt.printPersistentWhileThinking(rt.ui.statusKV("review_provider_failures", fmt.Sprintf("%d", run.Summary.ReviewProviderFailures)))
	}
	if run.Summary.ReviewQualityIssues > 0 {
		rt.printPersistentWhileThinking(rt.ui.statusKV("review_quality_issues", fmt.Sprintf("%d", run.Summary.ReviewQualityIssues)))
	}
	reused := 0
	missed := 0
	missReasons := map[string]int{}
	for _, shard := range run.Shards {
		if shard.CacheStatus == "reused" {
			reused++
		} else {
			missed++
			reason := strings.TrimSpace(shard.InvalidationReason)
			if reason == "" {
				reason = "unknown"
			}
			missReasons[reason]++
		}
	}
	rt.printPersistentWhileThinking(rt.ui.statusKV("cache_reused", fmt.Sprintf("%d", reused)))
	rt.printPersistentWhileThinking(rt.ui.statusKV("cache_miss", fmt.Sprintf("%d", missed)))
	if len(missReasons) > 0 {
		reasons := make([]string, 0, len(missReasons))
		for reason, count := range missReasons {
			reasons = append(reasons, fmt.Sprintf("%s=%d", reason, count))
		}
		slices.Sort(reasons)
		rt.printPersistentWhileThinking(rt.ui.statusKV("cache_miss_reasons", strings.Join(reasons, ", ")))
	}
	rt.printPersistentWhileThinking(rt.ui.statusKV("output", run.Summary.OutputPath))
	rt.printPersistentWhileThinking(rt.ui.statusKV("dashboard", filepath.Join(analysisCfg.OutputDir, "latest", "dashboard.html")))
	docsManifest, docsManifestOK := loadLatestAnalysisDocsManifest(rt.workspace.BaseRoot)
	rt.printPersistentWhileThinking(rt.ui.activityLine("analysis", "Recording analysis docs reuse artifacts..."))
	if docsManifestOK {
		if recordErr := rt.recordLatestAnalysisDocsArtifacts(run, docsManifest, analysisCfg.OutputDir); recordErr != nil {
			rt.printPersistentWhileThinking(rt.ui.warnLine("Could not record analysis docs reuse artifacts: " + recordErr.Error()))
		}
	} else {
		rt.printPersistentWhileThinking(rt.ui.warnLine("Could not read latest analysis docs manifest for reuse records."))
	}
	rt.printPersistentWhileThinking(rt.ui.activityLine("analysis", "Printing final analysis document..."))
	rt.printAssistant(run.FinalDocument)
	if artifacts := renderAnalysisProjectArtifactPathsStyled(run, analysisCfg.OutputDir, rt.ui); strings.TrimSpace(artifacts) != "" {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, artifacts)
	}
	handoff := renderAnalysisProjectHandoff(buildAnalysisProjectHandoff(run, docsManifest, docsManifestOK))
	if strings.TrimSpace(handoff) != "" {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, handoff)
	}
	return nil
}

func (rt *runtimeState) handleAnalyzeDashboardCommand(args string) error {
	analysisCfg := configProjectAnalysis(rt.cfg, rt.workspace.BaseRoot)
	target := strings.TrimSpace(args)
	dashboardPath := filepath.Join(analysisCfg.OutputDir, "latest", "dashboard.html")
	if target != "" && !strings.EqualFold(target, "latest") {
		dashboardPath = target
		if !filepath.IsAbs(dashboardPath) {
			if resolved, err := rt.workspace.ResolveForLookup(target); err == nil {
				if _, statErr := os.Stat(resolved); statErr == nil {
					dashboardPath = resolved
				} else {
					dashboardPath = filepath.Join(analysisCfg.OutputDir, target)
				}
			} else {
				dashboardPath = filepath.Join(analysisCfg.OutputDir, target)
			}
		}
		if info, err := os.Stat(dashboardPath); err == nil && info.IsDir() {
			dashboardPath = filepath.Join(dashboardPath, "dashboard.html")
		}
	}
	if _, err := os.Stat(dashboardPath); err != nil {
		return fmt.Errorf("analysis dashboard not found: %s; run /analyze-project first", dashboardPath)
	}
	if err := OpenExternalURL(dashboardPath); err != nil {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("Found analysis dashboard but could not open it automatically: "+err.Error()))
	} else {
		fmt.Fprintln(rt.writer, rt.ui.successLine("Opened analysis dashboard: "+dashboardPath))
	}
	return nil
}

func (rt *runtimeState) handleDocsRefreshCommand(args string) error {
	analysisCfg := configProjectAnalysis(rt.cfg, rt.workspace.BaseRoot)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	runPath := filepath.Join(latestDir, "run.json")
	data, err := os.ReadFile(runPath)
	if err != nil {
		return fmt.Errorf("latest analysis run not found; run /analyze-project first")
	}
	run := ProjectAnalysisRun{}
	if err := json.Unmarshal(data, &run); err != nil {
		return fmt.Errorf("read latest analysis run: %w", err)
	}
	docsManifest, err := writeAnalysisDocs(run, filepath.Join(latestDir, "docs"))
	if err != nil {
		return err
	}
	manifestData, err := json.MarshalIndent(docsManifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(latestDir, "docs_manifest.json"), manifestData, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(latestDir, "docs_index.md"), []byte(buildAnalysisDocs(run)["INDEX.md"]), 0o644); err != nil {
		return err
	}
	if err := writeAnalysisDashboard(run, filepath.Join(latestDir, "dashboard.html"), "docs"); err != nil {
		return err
	}
	run.VectorCorpus = buildVectorCorpus(run)
	run.VectorIngestion = buildVectorIngestionManifest(run.VectorCorpus)
	if err := writeVectorCorpusArtifactSet(run.VectorCorpus, run.VectorIngestion, latestDir, ""); err != nil {
		return err
	}
	refreshedRunData, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(runPath, refreshedRunData, 0o644); err != nil {
		return err
	}
	if err := rt.recordLatestAnalysisDocsArtifacts(run, docsManifest, analysisCfg.OutputDir); err != nil {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("Could not record analysis docs reuse artifacts: "+err.Error()))
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("Refreshed latest analysis docs."))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("docs", filepath.Join(latestDir, "docs")))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("manifest", filepath.Join(latestDir, "docs_manifest.json")))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("dashboard", filepath.Join(latestDir, "dashboard.html")))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("vector_corpus", filepath.Join(latestDir, "vector_corpus.jsonl")))
	return nil
}

func (rt *runtimeState) handleAnalyzePerformanceCommand(args string) error {
	if rt.agent == nil || rt.agent.Client == nil {
		return fmt.Errorf("no model provider is configured")
	}
	analysisCfg := configProjectAnalysis(rt.cfg, rt.workspace.BaseRoot)
	latestDir := filepath.Join(analysisCfg.OutputDir, "latest")
	knowledgePath := filepath.Join(latestDir, "knowledge_pack.json")
	perfPath := filepath.Join(latestDir, "performance_lens.json")

	knowledgeData, err := os.ReadFile(knowledgePath)
	if err != nil {
		return fmt.Errorf("latest knowledge pack not found; run /analyze-project first")
	}
	perfData, err := os.ReadFile(perfPath)
	if err != nil {
		return fmt.Errorf("latest performance lens not found; run /analyze-project first")
	}

	pack := KnowledgePack{}
	if err := json.Unmarshal(knowledgeData, &pack); err != nil {
		return fmt.Errorf("read knowledge pack: %w", err)
	}
	lens := PerformanceLens{}
	if err := json.Unmarshal(perfData, &lens); err != nil {
		return fmt.Errorf("read performance lens: %w", err)
	}

	fmt.Fprintln(rt.writer, rt.ui.section("Performance Analysis"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("workspace", rt.session.WorkingDir))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("knowledge_pack", knowledgePath))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("performance_lens", perfPath))
	if strings.TrimSpace(lens.PrimaryStartup) != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("primary_startup", lens.PrimaryStartup))
	}
	fmt.Fprintln(rt.writer)

	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopEscapeWatcher := startEscapeWatcher(cancel, rt.shouldHonorRequestCancel, rt.confirmRequestCancel)
	defer stopEscapeWatcher()

	rt.startThinkingIndicator()
	defer rt.stopThinkingIndicator()

	hotspotContext := rt.collectPerformanceHotspotContext(pack, lens)
	prompt := buildPerformanceAnalysisPrompt(pack, lens, hotspotContext, args)
	reply, err := rt.runAgentReply(requestCtx, prompt)
	if err != nil {
		reply = fallbackPerformanceReport(pack, lens, hotspotContext, args)
	}

	result := buildPerformanceAnalysisResult(pack, lens, args, knowledgePath, perfPath, reply)
	reportPath, jsonPath, pathErr := rt.persistPerformanceReport(reply, result, args, analysisCfg.OutputDir)
	if pathErr == nil {
		rt.printPersistentWhileThinking(rt.ui.statusKV("output", reportPath))
		rt.printPersistentWhileThinking(rt.ui.statusKV("output_json", jsonPath))
	}
	rt.printAssistant(reply)
	if handoff := performanceHandoff(result); strings.TrimSpace(handoff) != "" {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, handoff)
	}
	return nil
}

type PerformanceAnalysisResult struct {
	GeneratedAt     time.Time            `json:"generated_at"`
	Focus           string               `json:"focus,omitempty"`
	PrimaryStartup  string               `json:"primary_startup,omitempty"`
	StartupEntries  []string             `json:"startup_entries,omitempty"`
	TopHotspots     []PerformanceHotspot `json:"top_hotspots,omitempty"`
	ProfilingOrder  []string             `json:"profiling_order,omitempty"`
	SourceArtifacts []string             `json:"source_artifacts,omitempty"`
	Report          string               `json:"report"`
}

func buildPerformanceAnalysisResult(pack KnowledgePack, lens PerformanceLens, focus string, knowledgePath string, perfPath string, report string) PerformanceAnalysisResult {
	order := []string{}
	order = append(order, limitStrings(lens.HeavyEntrypoints, 4)...)
	order = append(order, limitStrings(lens.IOBoundCandidates, 3)...)
	order = append(order, limitStrings(lens.CPUBoundCandidates, 3)...)
	order = analysisUniqueStrings(order)
	return PerformanceAnalysisResult{
		GeneratedAt:     time.Now(),
		Focus:           strings.TrimSpace(focus),
		PrimaryStartup:  lens.PrimaryStartup,
		StartupEntries:  append([]string(nil), lens.StartupEntryFiles...),
		TopHotspots:     limitStringsPerformanceHotspots(lens.Hotspots, 10),
		ProfilingOrder:  order,
		SourceArtifacts: []string{knowledgePath, perfPath},
		Report:          report,
	}
}

func buildPerformanceAnalysisPrompt(pack KnowledgePack, lens PerformanceLens, hotspotContext string, focus string) string {
	var b strings.Builder
	b.WriteString("Analyze likely performance bottlenecks for this project using the provided architecture knowledge and performance lens.\n\n")
	if strings.TrimSpace(focus) != "" {
		fmt.Fprintf(&b, "Focus: %s\n\n", strings.TrimSpace(focus))
	}
	b.WriteString("Requirements:\n")
	b.WriteString("- Prioritize concrete bottleneck candidates over generic advice.\n")
	b.WriteString("- Distinguish startup, steady-state, IO-bound, CPU-bound, and memory-pressure issues when visible.\n")
	b.WriteString("- Mention which modules or files should be profiled first.\n")
	b.WriteString("- Use the hotspot file excerpts and extracted performance signals to call out concrete loops, parsers, ETW callbacks, upload/compression paths, synchronization hotspots, or large execution hubs when visible.\n")
	b.WriteString("- Recommend a short investigation order.\n\n")
	knowledgeJSON, _ := json.MarshalIndent(pack, "", "  ")
	perfJSON, _ := json.MarshalIndent(lens, "", "  ")
	b.WriteString("Knowledge Pack:\n")
	b.Write(knowledgeJSON)
	b.WriteString("\n\nPerformance Lens:\n")
	b.Write(perfJSON)
	if strings.TrimSpace(hotspotContext) != "" {
		b.WriteString("\n\nHotspot File Context:\n")
		b.WriteString(hotspotContext)
	}
	return b.String()
}

func fallbackPerformanceReport(pack KnowledgePack, lens PerformanceLens, hotspotContext string, focus string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Performance Bottleneck Analysis\n\n")
	if strings.TrimSpace(focus) != "" {
		fmt.Fprintf(&b, "- Focus: %s\n", strings.TrimSpace(focus))
	}
	if strings.TrimSpace(lens.PrimaryStartup) != "" {
		fmt.Fprintf(&b, "- Primary startup project: `%s`\n", lens.PrimaryStartup)
	}
	if len(lens.StartupEntryFiles) > 0 {
		fmt.Fprintf(&b, "- Startup entry files: %s\n", strings.Join(limitStrings(lens.StartupEntryFiles, 3), ", "))
	}
	b.WriteString("\n## Likely Hotspots\n\n")
	if len(lens.Hotspots) == 0 {
		b.WriteString("- No performance hotspots were inferred from the current knowledge pack.\n\n")
	} else {
		for _, hotspot := range limitStringsPerformanceHotspots(lens.Hotspots, 8) {
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
				fmt.Fprintf(&b, "- Files to profile first: %s\n", strings.Join(limitStrings(hotspot.Files, 4), "; "))
			}
			if snippet := hotspotSnippetForReport(hotspotContext, hotspot.Title); strings.TrimSpace(snippet) != "" {
				fmt.Fprintf(&b, "- Code context: %s\n", snippet)
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("## Profiling Order\n\n")
	order := []string{}
	order = append(order, limitStrings(lens.HeavyEntrypoints, 4)...)
	order = append(order, limitStrings(lens.IOBoundCandidates, 3)...)
	order = append(order, limitStrings(lens.CPUBoundCandidates, 3)...)
	order = analysisUniqueStrings(order)
	if len(order) == 0 {
		b.WriteString("- Start with the primary startup path and the largest subsystem files.\n")
	} else {
		for _, item := range order {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	b.WriteString("\n## Large Files\n\n")
	for _, item := range limitStrings(lens.LargeFiles, 10) {
		fmt.Fprintf(&b, "- %s\n", item)
	}
	return strings.TrimSpace(b.String()) + "\n"
}

func (rt *runtimeState) collectPerformanceHotspotContext(pack KnowledgePack, lens PerformanceLens) string {
	files := []string{}
	files = append(files, limitStrings(lens.StartupEntryFiles, 3)...)
	for _, hotspot := range limitStringsPerformanceHotspots(lens.Hotspots, 8) {
		files = append(files, limitStrings(hotspot.Files, 3)...)
	}
	for _, subsystem := range pack.Subsystems {
		if len(files) >= 18 {
			break
		}
		if strings.Contains(strings.ToLower(canonicalKnowledgeTitle(subsystem)), "performance") {
			files = append(files, limitStrings(subsystem.KeyFiles, 2)...)
		}
	}
	files = analysisUniqueStrings(files)

	sections := []string{}
	for _, rel := range limitStrings(files, 16) {
		abs, err := rt.workspace.Resolve(rel)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		lines := splitLines(string(data))
		if len(lines) > 80 {
			lines = lines[:80]
		}
		signals := detectLocalPerformanceSignals(string(data))
		localScore := scoreLocalPerformanceSignals(string(data))
		functions := detectHotFunctions(string(data))
		section := fmt.Sprintf("FILE %s\n", rel)
		if len(signals) > 0 {
			section += "Signals: " + strings.Join(limitStrings(signals, 8), ", ") + "\n"
		}
		if localScore > 0 {
			section += fmt.Sprintf("Local Score: %d\n", localScore)
		}
		if len(functions) > 0 {
			section += "Hot Functions: " + strings.Join(limitStrings(functions, 5), ", ") + "\n"
		}
		contextLines := lines
		if len(functions) > 0 {
			if narrowed := extractFunctionContext(string(data), functions[0], 60); len(narrowed) > 0 {
				contextLines = narrowed
			}
		}
		section += "```\n" + strings.Join(contextLines, "\n") + "\n```"
		sections = append(sections, section)
	}
	return strings.Join(sections, "\n\n")
}

func hotspotSnippetForReport(hotspotContext string, title string) string {
	if strings.TrimSpace(hotspotContext) == "" {
		return ""
	}
	lines := splitLines(hotspotContext)
	if len(lines) == 0 {
		return ""
	}
	titleLower := strings.ToLower(strings.TrimSpace(title))
	if titleLower == "" {
		return ""
	}
	for i, line := range lines {
		if strings.HasPrefix(line, "FILE ") && strings.Contains(strings.ToLower(line), titleLower) {
			end := i + 6
			if end > len(lines) {
				end = len(lines)
			}
			return strings.Join(lines[i:end], " ")
		}
	}
	for i, line := range lines {
		if strings.HasPrefix(line, "FILE ") {
			end := i + 4
			if end > len(lines) {
				end = len(lines)
			}
			return strings.Join(lines[i:end], " ")
		}
	}
	return ""
}

func detectLocalPerformanceSignals(content string) []string {
	lower := strings.ToLower(content)
	signals := []string{}
	patterns := []struct {
		label   string
		markers []string
	}{
		{label: "tight loops", markers: []string{"for (", "while (", "do {", "for("}},
		{label: "sleep or polling", markers: []string{"sleep(", "sleep_for(", "waitforsingleobject(", "peekmessage(", "getmessage("}},
		{label: "locking or contention", markers: []string{"critical_section", "std::mutex", "lock_guard", "unique_lock", "entercriticalsection", "srwlock"}},
		{label: "heap allocation churn", markers: []string{"new ", "delete ", "malloc(", "free(", "realloc(", "vector<", "push_back("}},
		{label: "memory copy or buffer churn", markers: []string{"memcpy(", "memmove(", "copy(", "append(", "resize("}},
		{label: "file io", markers: []string{"createfile", "readfile(", "writefile(", "ifstream", "ofstream", "fread(", "fwrite("}},
		{label: "network io", markers: []string{"send(", "recv(", "connect(", "winhttp", "internetreadfile", "socket("}},
		{label: "compression or crypto", markers: []string{"compress", "decompress", "zip", "inflate", "deflate", "sha1", "sha256", "aes", "bcrypt"}},
		{label: "etw or callback dispatch", markers: []string{"etw", "eventrecordcallback", "processtrace(", "enabletraceex2", "open trace"}},
		{label: "thread fan-out", markers: []string{"std::thread", "_beginthreadex", "createthread(", "queueuserworkitem("}},
	}
	for _, pattern := range patterns {
		if containsAny(lower, pattern.markers...) {
			signals = append(signals, pattern.label)
		}
	}
	return analysisUniqueStrings(signals)
}

func scoreLocalPerformanceSignals(content string) int {
	signals := detectLocalPerformanceSignals(content)
	score := 0
	for _, signal := range signals {
		switch signal {
		case "tight loops":
			score += 3
		case "sleep or polling":
			score += 4
		case "locking or contention":
			score += 5
		case "heap allocation churn":
			score += 4
		case "memory copy or buffer churn":
			score += 4
		case "file io":
			score += 4
		case "network io":
			score += 4
		case "compression or crypto":
			score += 4
		case "etw or callback dispatch":
			score += 4
		case "thread fan-out":
			score += 3
		}
	}
	lower := strings.ToLower(content)
	if containsAny(lower, "retry", "backoff", "poll", "busy", "spin") {
		score += 3
	}
	if containsAny(lower, "printf(", "outputdebugstring", "spdlog", "log(", "trace(", "securelog") {
		score += 2
	}
	return score
}

func detectHotFunctions(content string) []string {
	lines := splitLines(content)
	out := []string{}
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !looksLikeFunctionSignature(trimmed) {
			continue
		}
		start := i
		if start > 0 && strings.TrimSpace(lines[start-1]) != "" && !strings.HasSuffix(strings.TrimSpace(lines[start-1]), ";") {
			prev := strings.TrimSpace(lines[start-1])
			if strings.Contains(prev, "(") || strings.Contains(prev, "::") {
				start = start - 1
			}
		}
		end := analysisMinInt(len(lines), i+40)
		window := strings.Join(lines[start:end], "\n")
		signals := detectLocalPerformanceSignals(window)
		if len(signals) == 0 {
			continue
		}
		if name := functionNameFromSignature(trimmed); name != "" {
			score := scoreLocalPerformanceSignals(window)
			out = append(out, fmt.Sprintf("%s [score=%d; %s]", name, score, strings.Join(limitStrings(signals, 3), ", ")))
		}
	}
	return analysisUniqueStrings(out)
}

func looksLikeFunctionSignature(line string) bool {
	if line == "" {
		return false
	}
	if strings.HasPrefix(line, "#") {
		return false
	}
	if strings.HasPrefix(line, "if ") || strings.HasPrefix(line, "if(") ||
		strings.HasPrefix(line, "for ") || strings.HasPrefix(line, "for(") ||
		strings.HasPrefix(line, "while ") || strings.HasPrefix(line, "while(") ||
		strings.HasPrefix(line, "switch ") || strings.HasPrefix(line, "switch(") {
		return false
	}
	if !strings.Contains(line, "(") || !strings.Contains(line, ")") {
		return false
	}
	if !(strings.HasSuffix(line, "{") || strings.Contains(line, ") const") || strings.Contains(line, ") noexcept") || strings.Contains(line, ")")) {
		return false
	}
	return true
}

func functionNameFromSignature(line string) string {
	open := strings.Index(line, "(")
	if open <= 0 {
		return ""
	}
	prefix := strings.TrimSpace(line[:open])
	fields := strings.Fields(prefix)
	if len(fields) == 0 {
		return ""
	}
	name := fields[len(fields)-1]
	name = strings.Trim(name, "*&")
	if name == "" {
		return ""
	}
	return name
}

func extractFunctionContext(content string, functionLabel string, limit int) []string {
	name := strings.TrimSpace(functionLabel)
	if idx := strings.Index(name, " ["); idx >= 0 {
		name = name[:idx]
	}
	if name == "" {
		return nil
	}
	lines := splitLines(content)
	start := -1
	for i, line := range lines {
		if strings.Contains(line, name+"(") || strings.Contains(line, name+" (") {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	end := analysisMinInt(len(lines), start+limit)
	return append([]string(nil), lines[start:end]...)
}

func (rt *runtimeState) persistPerformanceReport(report string, result PerformanceAnalysisResult, focus string, outputDir string) (string, string, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", "", err
	}
	name := "performance"
	if strings.TrimSpace(focus) != "" {
		name += "_" + sanitizeFileName(focus)
	}
	base := filepath.Join(outputDir, time.Now().Format("20060102-150405")+"_"+name)
	path := base + ".md"
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		return "", "", err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", "", err
	}
	jsonPath := base + ".json"
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		return "", "", err
	}
	return path, jsonPath, nil
}

func (rt *runtimeState) closeExtensions() {
	if rt.mcp != nil {
		rt.mcp.Close()
		rt.mcp = nil
	}
}

func (rt *runtimeState) reloadRuntimeConfig() error {
	loaded, err := LoadConfigWithOptions(rt.workspace.BaseRoot, ConfigLoadOptions{
		StrictConfig: rt.strictConfig,
		Profile:      rt.configProfile,
	})
	if err != nil {
		return err
	}
	// Run the same legacy-defaults migration as startup. /reload after a
	// fresh KernForge upgrade is one of the few times we get a second chance
	// to catch lingering legacy values without the user manually editing.
	if notices := MigrateLegacyConfigDefaultsWithProfile(rt.workspace.BaseRoot, rt.configProfile, &loaded); len(notices) > 0 {
		rt.printLegacyConfigMigrationNotices(notices)
	}
	loaded.BypassHookTrust = configBypassHookTrust(rt.cfg)

	activeProvider := rt.session.Provider
	if strings.EqualFold(strings.TrimSpace(activeProvider), strings.TrimSpace(rt.cfg.Provider)) {
		activeProvider = loaded.Provider
	}
	activeModel := rt.session.Model
	if strings.TrimSpace(activeModel) == strings.TrimSpace(rt.cfg.Model) {
		activeModel = loaded.Model
	}
	activeBaseURL := rt.session.BaseURL
	if normalizeProfileBaseURL(rt.session.Provider, activeBaseURL) == normalizeProfileBaseURL(rt.cfg.Provider, rt.cfg.BaseURL) {
		activeBaseURL = loaded.BaseURL
	}
	activePermission := rt.activePermissionModeSnapshot()
	if samePermissionMode(activePermission, rt.cfg.PermissionMode) {
		activePermission = loaded.PermissionMode
	}
	if mode, ok := ParseModeStrict(activePermission); ok {
		activePermission = string(mode)
	} else {
		activePermission = loaded.PermissionMode
	}

	rt.cfg = loaded
	rt.session.Provider = activeProvider
	rt.session.Model = activeModel
	rt.session.BaseURL = activeBaseURL
	rt.session.PermissionMode = activePermission
	rt.perms.SetMode(ParseMode(rt.session.PermissionMode))
	rt.store = NewSessionStore(rt.cfg.SessionDir)
	rt.syncWorkspaceFromSession()

	rt.memory, err = LoadMemory(rt.workspace.BaseRoot, rt.cfg.MemoryFiles)
	if err != nil {
		return err
	}
	rt.reloadHooks()
	if rt.agent != nil {
		rt.agent.Config = rt.cfg
		rt.agent.Memory = rt.memory
		rt.agent.Store = rt.store
		rt.agent.Session = rt.session
		rt.agent.Workspace = rt.workspace
		rt.agent.LongMem = rt.longMem
	}
	rt.reloadExtensions()
	rt.syncClientFromConfig()
	return rt.store.Save(rt.session)
}

func samePermissionMode(a, b string) bool {
	return ParseMode(a) == ParseMode(b)
}

func (rt *runtimeState) activePermissionModeSnapshot() string {
	if rt != nil && rt.perms != nil {
		return string(rt.perms.Mode())
	}
	if rt != nil && rt.session != nil {
		return rt.session.PermissionMode
	}
	return ""
}

func (rt *runtimeState) reloadExtensions() {
	rt.skills, rt.skillWarns = LoadSkills(rt.workspace.BaseRoot, rt.cfg.SkillPaths, rt.cfg.EnabledSkills)
	if rt.agent != nil {
		rt.agent.Skills = rt.skills
	}
	if rt.mcp != nil {
		rt.mcp.Close()
	}
	rt.mcp, rt.mcpWarns = LoadMCPManager(rt.workspace, rt.cfg.MCPServers)
	if rt.agent != nil {
		rt.agent.MCP = rt.mcp
		rt.agent.Tools = buildRegistry(rt.workspace, rt.mcp)
		for _, issue := range formatToolRegistrationIssues(rt.agent.Tools.RegistrationIssues()) {
			fmt.Fprintln(rt.writer, rt.ui.warnLine("Tool registry: "+issue))
		}
	}
}

func (rt *runtimeState) reloadHooks() {
	engine, warns := LoadHookEngine(rt.workspace.BaseRoot, rt.cfg)
	rt.hookWarns = warns
	for _, warn := range warns {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("Hook config: "+warn))
	}
	if engine == nil {
		rt.hooks = nil
		return
	}
	rt.hooks = &HookRuntime{
		Engine: engine,
		Ask:    rt.confirm,
		Print:  func(text string) { fmt.Fprintln(rt.writer, rt.ui.warnLine(text)) },
		CreateCheckpoint: func(note string) (CheckpointMetadata, error) {
			if rt.checkpoints == nil {
				return CheckpointMetadata{}, fmt.Errorf("checkpoint manager is not configured")
			}
			return rt.checkpoints.Create(workspaceCheckpointRoot(rt.workspace), note)
		},
		FailClosed: configHooksFailClosed(rt.cfg),
		Workspace:  rt.workspace,
		Session:    rt.session,
		Config:     rt.cfg,
		Evidence:   rt.evidence,
		Overrides:  rt.hookOverrides,
	}
}

func joinHookEvents(events []HookEvent) string {
	var parts []string
	for _, event := range events {
		parts = append(parts, string(event))
	}
	return strings.Join(parts, ",")
}

func mcpStatusEnvironmentID(status MCPServerStatus) string {
	environmentID := strings.TrimSpace(status.EnvironmentID)
	if environmentID == "" {
		return defaultMCPServerEnvironmentID
	}
	return environmentID
}

func (rt *runtimeState) runHook(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
	if rt.hooks == nil {
		return HookVerdict{Allow: true}, nil
	}
	rt.hooks.Workspace = rt.workspace
	rt.hooks.Session = rt.session
	rt.hooks.Config = rt.cfg
	rt.hooks.FailClosed = configHooksFailClosed(rt.cfg)
	rt.hooks.Evidence = rt.evidence
	return rt.hooks.Run(ctx, event, payload)
}

func (rt *runtimeState) mcpStatus() []MCPServerStatus {
	if rt.mcp == nil {
		return nil
	}
	return rt.mcp.Status()
}

func (rt *runtimeState) mcpToolCount() int {
	total := 0
	for _, status := range rt.mcpStatus() {
		total += status.ToolCount
	}
	return total
}

func (rt *runtimeState) mcpResourceCount() int {
	total := 0
	for _, status := range rt.mcpStatus() {
		total += status.ResourceCount
	}
	return total
}

func (rt *runtimeState) mcpPromptCount() int {
	total := 0
	for _, status := range rt.mcpStatus() {
		total += status.PromptCount
	}
	return total
}

func (rt *runtimeState) mcpResources() []MCPResourceRef {
	if rt.mcp == nil {
		return nil
	}
	return rt.mcp.Resources()
}

func (rt *runtimeState) mcpPrompts() []MCPPromptRef {
	if rt.mcp == nil {
		return nil
	}
	return rt.mcp.Prompts()
}

type analyzeProjectCommandArgs struct {
	Mode  string
	Goal  string
	Paths []string
}

func parseAnalyzeProjectCommandArgs(raw string) (analyzeProjectCommandArgs, error) {
	trimmed := strings.TrimSpace(raw)
	fields := strings.Fields(trimmed)
	mode := ""
	goalParts := []string{}
	paths := []string{}
	for index := 0; index < len(fields); index++ {
		field := strings.TrimSpace(fields[index])
		switch {
		case strings.HasPrefix(field, "--mode="):
			value := strings.TrimSpace(strings.TrimPrefix(field, "--mode="))
			mode = normalizeProjectAnalysisMode(value)
			if mode == "" {
				return analyzeProjectCommandArgs{}, fmt.Errorf("invalid analyze-project mode %q; expected one of %s", value, strings.Join(supportedProjectAnalysisModes, ", "))
			}
		case field == "--mode":
			if index+1 >= len(fields) {
				return analyzeProjectCommandArgs{}, fmt.Errorf(projectAnalysisUsage())
			}
			index++
			value := strings.TrimSpace(fields[index])
			mode = normalizeProjectAnalysisMode(value)
			if mode == "" {
				return analyzeProjectCommandArgs{}, fmt.Errorf("invalid analyze-project mode %q; expected one of %s", value, strings.Join(supportedProjectAnalysisModes, ", "))
			}
		case strings.HasPrefix(field, "--path="):
			value := strings.TrimSpace(strings.TrimPrefix(field, "--path="))
			if value == "" {
				return analyzeProjectCommandArgs{}, fmt.Errorf(projectAnalysisUsage())
			}
			paths = append(paths, value)
		case field == "--path":
			if index+1 >= len(fields) {
				return analyzeProjectCommandArgs{}, fmt.Errorf(projectAnalysisUsage())
			}
			index++
			value := strings.TrimSpace(fields[index])
			if value == "" {
				return analyzeProjectCommandArgs{}, fmt.Errorf(projectAnalysisUsage())
			}
			paths = append(paths, value)
		case field == "--docs":
			// Accepted for explicit documentation generation; docs are written by default.
		default:
			goalParts = append(goalParts, field)
		}
	}
	goal := strings.TrimSpace(strings.Join(goalParts, " "))
	if goal == "" {
		goal = defaultProjectAnalysisGoal(mode, paths)
	}
	return analyzeProjectCommandArgs{Mode: mode, Goal: goal, Paths: paths}, nil
}

func parseAnalyzeProjectArgs(raw string) (string, string, error) {
	parsed, err := parseAnalyzeProjectCommandArgs(raw)
	if err != nil {
		return "", "", err
	}
	return parsed.Mode, parsed.Goal, nil
}

func prepareExplicitAnalysisWorkspace(ws Workspace, paths []string) (Workspace, []string, error) {
	if len(paths) != 1 {
		return ws, paths, nil
	}
	requested := strings.TrimSpace(paths[0])
	if requested == "" {
		return ws, paths, nil
	}
	baseRoot := strings.TrimSpace(ws.Root)
	if baseRoot == "" {
		baseRoot = strings.TrimSpace(ws.BaseRoot)
	}
	if baseRoot == "" {
		return ws, paths, nil
	}
	target := requested
	if !filepath.IsAbs(target) {
		target = filepath.Join(baseRoot, filepath.FromSlash(target))
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return ws, paths, nil
	}
	info, err := os.Stat(absTarget)
	if err != nil || !info.IsDir() {
		return ws, paths, nil
	}
	absBase, err := filepath.Abs(baseRoot)
	if err != nil {
		return ws, paths, nil
	}
	rel, err := filepath.Rel(absBase, absTarget)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return ws, paths, nil
	}
	updated := ws
	updated.Root = absTarget
	return updated, nil, nil
}

func resolveExplicitAnalysisScope(paths []string, snapshot ProjectSnapshot) (AnalysisGoalScope, []string) {
	if len(paths) == 0 {
		return AnalysisGoalScope{}, nil
	}
	directories := analysisScopeDirectories(snapshot)
	prefixes := []string{}
	unmatched := []string{}
	for _, raw := range paths {
		candidate := normalizeAnalysisPathArgument(raw, snapshot.Root)
		if candidate == "" {
			continue
		}
		if analysisPathMatchesSnapshot(candidate, snapshot, directories) {
			prefixes = append(prefixes, candidate)
			continue
		}
		unmatched = append(unmatched, raw)
	}
	return AnalysisGoalScope{DirectoryPrefixes: compactAnalysisScopePrefixes(prefixes)}, unmatched
}

func normalizeAnalysisPathArgument(raw string, root string) string {
	trimmed := strings.Trim(strings.TrimSpace(raw), "\"'")
	if trimmed == "" {
		return ""
	}
	cleaned := filepath.Clean(trimmed)
	if filepath.IsAbs(cleaned) && strings.TrimSpace(root) != "" {
		if rel, err := filepath.Rel(root, cleaned); err == nil && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			cleaned = rel
		}
	}
	slashed := filepath.ToSlash(cleaned)
	slashed = strings.TrimPrefix(slashed, "./")
	slashed = strings.Trim(slashed, "/")
	if slashed == "." {
		return ""
	}
	return slashed
}

func analysisPathMatchesSnapshot(candidate string, snapshot ProjectSnapshot, directories []string) bool {
	candidate = strings.ToLower(filepath.ToSlash(strings.Trim(candidate, "/")))
	if candidate == "" {
		return false
	}
	for _, dir := range directories {
		lowerDir := strings.ToLower(filepath.ToSlash(strings.Trim(dir, "/")))
		if lowerDir == candidate || strings.HasPrefix(lowerDir+"/", candidate+"/") || strings.HasPrefix(candidate+"/", lowerDir+"/") {
			return true
		}
	}
	for _, file := range snapshot.Files {
		lowerFile := strings.ToLower(filepath.ToSlash(strings.Trim(file.Path, "/")))
		if lowerFile == candidate || strings.HasPrefix(lowerFile, candidate+"/") {
			return true
		}
	}
	for dir, files := range snapshot.FilesByDirectory {
		lowerDir := strings.ToLower(filepath.ToSlash(strings.Trim(dir, "/")))
		if lowerDir == candidate || strings.HasPrefix(lowerDir+"/", candidate+"/") {
			return true
		}
		if len(files) > 0 && strings.HasPrefix(candidate+"/", lowerDir+"/") {
			return true
		}
	}
	return false
}

func parsePromptCommandArgs(raw string) (string, map[string]any, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil, fmt.Errorf("usage: /prompt <server:prompt-name> [json-arguments]")
	}
	target := trimmed
	rest := ""
	if idx := strings.IndexAny(trimmed, " \t"); idx >= 0 {
		target = strings.TrimSpace(trimmed[:idx])
		rest = strings.TrimSpace(trimmed[idx+1:])
	}
	args := map[string]any{}
	if rest == "" {
		return target, args, nil
	}
	if err := json.Unmarshal([]byte(rest), &args); err != nil {
		return "", nil, fmt.Errorf("prompt arguments must be a JSON object: %w", err)
	}
	return target, args, nil
}

func (rt *runtimeState) persistentMemoryCount() int {
	if rt.longMem == nil {
		return 0
	}
	stats, err := rt.longMem.Stats()
	if err != nil {
		return 0
	}
	return stats.Count
}

func (rt *runtimeState) evidenceCount() int {
	if rt.evidence == nil {
		return 0
	}
	stats, err := rt.evidence.Stats()
	if err != nil {
		return 0
	}
	return stats.Count
}

func (rt *runtimeState) verificationHistoryCount() int {
	if rt.verifyHistory == nil {
		return 0
	}
	summary, err := rt.verifyHistory.Dashboard(workspaceSnapshotRoot(rt.workspace), true, nil, 1)
	if err != nil {
		return 0
	}
	return summary.TotalReports
}

func parseSelectionReviewArgs(sess *Session, raw string) ([]ViewerSelection, string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return append([]ViewerSelection(nil), sess.Selections...), "", nil
	}
	parts := strings.SplitN(trimmed, " -- ", 2)
	selectorPart := strings.TrimSpace(parts[0])
	extra := ""
	if len(parts) == 2 {
		extra = strings.TrimSpace(parts[1])
	}
	if selectorPart == "" || strings.EqualFold(selectorPart, "all") {
		return append([]ViewerSelection(nil), sess.Selections...), extra, nil
	}
	var selected []ViewerSelection
	for _, token := range strings.Split(selectorPart, ",") {
		value := strings.TrimSpace(token)
		if value == "" {
			continue
		}
		index, err := parsePositiveInt(value)
		if err != nil || index < 1 || index > len(sess.Selections) {
			return nil, "", fmt.Errorf("invalid selection index: %s", value)
		}
		selected = append(selected, sess.Selections[index-1])
	}
	if len(selected) == 0 {
		return nil, "", fmt.Errorf("no selections chosen")
	}
	return selected, extra, nil
}

func parseVerificationDashboardArgs(raw string) (bool, []string) {
	all := false
	var tags []string
	for _, token := range strings.Fields(strings.TrimSpace(raw)) {
		trimmed := strings.TrimSpace(token)
		switch {
		case strings.EqualFold(trimmed, "all"):
			all = true
		case strings.HasPrefix(strings.ToLower(trimmed), "tag:"):
			if tag := strings.TrimSpace(trimmed[4:]); tag != "" {
				tags = append(tags, tag)
			}
		}
	}
	return all, uniqueStrings(tags)
}

func (rt *runtimeState) armAutoCheckpoint() {
	if rt.autoCP == nil {
		return
	}
	rt.autoCP.Manager = rt.checkpoints
	rt.autoCP.Arm(configAutoCheckpointEdits(rt.cfg), workspaceCheckpointRoot(rt.workspace))
}

func (rt *runtimeState) clearAutoCheckpoint() {
	if rt.autoCP == nil {
		return
	}
	rt.autoCP.Clear()
}

func (rt *runtimeState) prepareEdit(reason string) error {
	if rt.autoCP == nil {
		return nil
	}
	if rt.autoCP.Enabled && rt.autoCP.Pending {
		if !rt.showTransientWhileThinking(rt.ui.infoLine("Creating automatic checkpoint before edit...")) {
			rt.printPersistentWhileThinking(rt.ui.infoLine("Creating automatic checkpoint before edit..."))
		}
	}
	meta, err := rt.autoCP.Prepare(reason)
	if err != nil {
		return err
	}
	if message := formatAutoCheckpointMessage(meta); message != "" {
		if !rt.showTransientWhileThinking(rt.ui.infoLine(message)) {
			rt.printPersistentWhileThinking(rt.ui.infoLine(message))
		}
	}
	return nil
}

func (rt *runtimeState) prepareEditAtRoot(reason string, root string) error {
	if rt == nil || rt.autoCP == nil {
		return nil
	}
	targetRoot := strings.TrimSpace(root)
	if targetRoot == "" || strings.EqualFold(targetRoot, workspaceCheckpointRoot(rt.workspace)) {
		return rt.prepareEdit(reason)
	}
	if rt.autoCP.Enabled && rt.autoCP.Pending {
		if !rt.showTransientWhileThinking(rt.ui.infoLine("Creating automatic checkpoint before edit...")) {
			rt.printPersistentWhileThinking(rt.ui.infoLine("Creating automatic checkpoint before edit..."))
		}
	}
	meta, err := rt.autoCP.PrepareAtWorkspace(reason, targetRoot)
	if err != nil {
		return err
	}
	if message := formatAutoCheckpointMessage(meta); message != "" {
		if !rt.showTransientWhileThinking(rt.ui.infoLine(message)) {
			rt.printPersistentWhileThinking(rt.ui.infoLine(message))
		}
	}
	return nil
}

func workspaceSnapshotRoot(ws Workspace) string {
	if strings.TrimSpace(ws.BaseRoot) != "" {
		return ws.BaseRoot
	}
	return ws.Root
}

func workspaceCheckpointRoot(ws Workspace) string {
	if strings.TrimSpace(ws.Root) != "" {
		return ws.Root
	}
	return workspaceSnapshotRoot(ws)
}

func workspaceEffectiveRoots(ws Workspace, sess *Session) []string {
	baseRoot := canonicalWorkspaceRootForMetadata(ws.BaseRoot)
	if baseRoot == "" && sess != nil {
		baseRoot = canonicalWorkspaceRootForMetadata(sessionBaseWorkingDir(sess))
	}
	if baseRoot == "" {
		baseRoot = canonicalWorkspaceRootForMetadata(ws.Root)
	}

	activeRoot := canonicalWorkspaceRootForMetadata(ws.Root)
	if activeRoot == "" && sess != nil {
		activeRoot = canonicalWorkspaceRootForMetadata(sess.WorkingDir)
	}
	if activeRoot == "" {
		activeRoot = baseRoot
	}

	roots := make([]string, 0, 2)
	appendRoot := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		for _, existing := range roots {
			if samePath(existing, path) {
				return
			}
		}
		roots = append(roots, path)
	}
	appendRoot(baseRoot)
	appendRoot(activeRoot)
	return roots
}

func canonicalWorkspaceRootForMetadata(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	cleaned := filepath.Clean(trimmed)
	if filepath.IsAbs(cleaned) {
		return cleaned
	}
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return cleaned
	}
	return filepath.Clean(abs)
}

func workspaceEffectiveBaseRoot(ws Workspace, sess *Session) string {
	roots := workspaceEffectiveRoots(ws, sess)
	if len(roots) == 0 {
		return ""
	}
	return roots[0]
}

func workspaceEffectiveActiveRoot(ws Workspace, sess *Session) string {
	activeRoot := canonicalWorkspaceRootForMetadata(ws.Root)
	if activeRoot == "" && sess != nil {
		activeRoot = canonicalWorkspaceRootForMetadata(sess.WorkingDir)
	}
	if activeRoot != "" {
		return activeRoot
	}
	roots := workspaceEffectiveRoots(ws, sess)
	if len(roots) == 0 {
		return ""
	}
	return roots[len(roots)-1]
}

func addEffectiveWorkspaceRootMetadata(meta map[string]any, ws Workspace, sess *Session) {
	if meta == nil {
		return
	}
	baseRoot := strings.TrimSpace(workspaceEffectiveBaseRoot(ws, sess))
	activeRoot := strings.TrimSpace(workspaceEffectiveActiveRoot(ws, sess))
	roots := workspaceEffectiveRoots(ws, sess)
	if baseRoot != "" {
		meta["workspace_root"] = baseRoot
	}
	if activeRoot != "" && (baseRoot == "" || !samePath(baseRoot, activeRoot)) {
		meta["active_workspace_root"] = activeRoot
	} else {
		delete(meta, "active_workspace_root")
	}
	if len(roots) > 0 {
		meta["workspace_roots"] = roots
	}
}

func addEffectivePermissionMetadata(meta map[string]any, ws Workspace, sess *Session) {
	if meta == nil {
		return
	}
	permissionMode := ""
	if ws.Perms != nil {
		permissionMode = string(ws.Perms.Mode())
	}
	if strings.TrimSpace(permissionMode) == "" && sess != nil {
		permissionMode = strings.TrimSpace(sess.PermissionMode)
	}
	permissionMode = strings.TrimSpace(permissionMode)
	if permissionMode == "" {
		return
	}
	meta["permission_mode"] = permissionMode
	if profileID := activePermissionProfileIDForModeString(permissionMode); profileID != "" {
		meta["active_permission_profile_id"] = profileID
		meta["active_permission_profile"] = activePermissionProfileSnapshotForModeString(permissionMode)
	}
	if sandbox := mcpTurnMetadataSandboxTag(permissionMode); sandbox != "" {
		meta["sandbox"] = sandbox
	}
}

func addEffectiveExecutionContextMetadata(meta map[string]any, ws Workspace, sess *Session) {
	addEffectiveWorkspaceRootMetadata(meta, ws, sess)
	addEffectivePermissionMetadata(meta, ws, sess)
}

func (rt *runtimeState) handleInitCommand(args string) error {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		path := filepath.Join(sessionBaseWorkingDir(rt.session), "KERNFORGE.md")
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists", path)
		}
		projectName := filepath.Base(sessionBaseWorkingDir(rt.session))
		if err := os.WriteFile(path, []byte(InitMemoryTemplate(projectName)), 0o644); err != nil {
			return err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Created "+path))
		rt.memory, _ = LoadMemory(sessionBaseWorkingDir(rt.session), rt.cfg.MemoryFiles)
		rt.agent.Memory = rt.memory
		return nil
	}

	fields := strings.Fields(trimmed)
	switch strings.ToLower(fields[0]) {
	case "skill":
		name := strings.TrimSpace(strings.TrimPrefix(trimmed, fields[0]))
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("usage: /init skill <name>")
		}
		if strings.ContainsAny(name, `\/`) {
			return fmt.Errorf("skill name must be a single path segment")
		}
		path := filepath.Join(rt.workspace.BaseRoot, userConfigDirName, "skills", name, "SKILL.md")
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists", path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(InitSkillTemplate(name)), 0o644); err != nil {
			return err
		}
		rt.reloadExtensions()
		fmt.Fprintln(rt.writer, rt.ui.successLine("Created "+path))
		return nil
	case "config":
		path := workspaceConfigPath(rt.workspace.BaseRoot)
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists", path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(InitWorkspaceConfigTemplate(rt.workspace.BaseRoot)), 0o644); err != nil {
			return err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Created "+path))
		return nil
	case "hooks":
		path := filepath.Join(rt.workspace.BaseRoot, userConfigDirName, "hooks.json")
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists", path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(InitHooksTemplate()), 0o644); err != nil {
			return err
		}
		rt.reloadHooks()
		fmt.Fprintln(rt.writer, rt.ui.successLine("Created "+path))
		return nil
	case "verify":
		path := filepath.Join(rt.workspace.BaseRoot, userConfigDirName, "verify.json")
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists", path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(InitVerifyPolicyTemplate()), 0o644); err != nil {
			return err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Created "+path))
		return nil
	case "memory-policy":
		path := filepath.Join(rt.workspace.BaseRoot, userConfigDirName, "memory-policy.json")
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists", path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(InitMemoryPolicyTemplate()), 0o644); err != nil {
			return err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Created "+path))
		return nil
	default:
		return fmt.Errorf("usage: /init [memory-policy|skill <name>|config|hooks|verify]")
	}
}
