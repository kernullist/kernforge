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
	hookOverrides              *HookOverrideStore
	checkpoints                *CheckpointManager
	autoCP                     *AutoCheckpointController
	verifyHistory              *VerificationHistoryStore
	backgroundJobs             *BackgroundJobManager
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
	interactive                bool
	outputMu                   sync.Mutex
	thinkingMu                 sync.Mutex
	thinkingStop               func()
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
	requestCancelMu            sync.Mutex
	requestCancelPauses        int
	requestCancelPending       bool
	requestCancelIgnoreUntil   time.Time
	lastAssistantMu            sync.Mutex
	lastAssistantPrinted       string
	alwaysApprovePreview       bool
	alwaysApproveWrites        bool
	autoAcceptPreviewOnce      bool
}

var assistantFollowOnPreamblePattern = regexp.MustCompile(`([\.\:\!\?\)])((?i:Now let me |Let me |Now I |I'll |I will |I need to |First, ))`)
var numberedPlanItemPattern = regexp.MustCompile(`^\d+[\.\)]\s+(.+)$`)
var bulletPlanItemPattern = regexp.MustCompile(`^[-*]\s+(.+)$`)

var fetchOpenRouterKeyStatus = FetchOpenRouterKeyStatus
var fetchOpenRouterCredits = FetchOpenRouterCredits

const openRouterCurrentKeyDocsURL = "https://openrouter.ai/docs/api/api-reference/api-keys/get-current-key"
const openRouterCreditsDocsURL = "https://openrouter.ai/docs/api/api-reference/credits/get-credits"
const anthropicBillingHelpURL = "https://support.anthropic.com/en/articles/8977456-how-do-i-pay-for-my-api-usage"
const anthropicUsageCostDocsURL = "https://platform.claude.com/docs/en/api/usage-cost-api"
const openAIUsageAPIDocsURL = "https://platform.openai.com/docs/api-reference/usage/costs?api-mode=responses&lang=curl"
const openAIPrepaidBillingHelpURL = "https://help.openai.com/en/articles/8264644-how-can-i-set-up-prepaid-billing"
const openAIUsageDashboardHelpURL = "https://help.openai.com/en/articles/10478918-api-usage-dashboard"

func run(args []string) error {
	fs := flag.NewFlagSet("kernforge", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		cwdFlag           string
		providerFlag      string
		modelFlag         string
		baseURLFlag       string
		imageFlag         string
		previewFileFlag   string
		previewResultFlag string
		viewerFileFlag    string
		viewerResultFlag  string
		promptFlag        string
		resumeFlag        string
		permissionFlag    string
		yesFlag           bool
	)

	fs.StringVar(&cwdFlag, "cwd", "", "working directory")
	fs.StringVar(&providerFlag, "provider", "", "model provider")
	fs.StringVar(&modelFlag, "model", "", "model name")
	fs.StringVar(&baseURLFlag, "base-url", "", "provider base URL")
	fs.StringVar(&imageFlag, "image", "", "comma-separated image paths for -prompt mode")
	fs.StringVar(&imageFlag, "i", "", "shorthand for -image")
	fs.StringVar(&previewFileFlag, "preview-file", "", "internal diff preview file path")
	fs.StringVar(&previewResultFlag, "preview-result-file", "", "internal diff preview result path")
	fs.StringVar(&viewerFileFlag, "viewer-file", "", "internal viewer file path")
	fs.StringVar(&viewerResultFlag, "viewer-result-file", "", "internal viewer result path")
	fs.StringVar(&promptFlag, "prompt", "", "single prompt mode")
	fs.StringVar(&resumeFlag, "resume", "", "resume session by id")
	fs.StringVar(&permissionFlag, "permission-mode", "", "permissions mode")
	fs.BoolVar(&yesFlag, "y", false, "auto-approve all permissions")

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

	cfg, err := LoadConfig(cwd)
	if err != nil {
		return err
	}
	if providerFlag != "" {
		cfg.Provider = providerFlag
	}
	if modelFlag != "" {
		cfg.Model = modelFlag
	}
	if baseURLFlag != "" {
		cfg.BaseURL = baseURLFlag
	}
	if permissionFlag != "" {
		cfg.PermissionMode = permissionFlag
	}
	if yesFlag {
		cfg.PermissionMode = string(ModeBypass)
	}
	if _, err := autoPopulateVerificationToolPaths(cwd, &cfg, detectWindowsVerificationToolPath); err != nil {
		return err
	}
	if strings.EqualFold(cfg.Provider, "ollama") && strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = normalizeOllamaBaseURL("")
	}

	mem, err := LoadMemory(cwd, cfg.MemoryFiles)
	if err != nil {
		return err
	}

	store := NewSessionStore(cfg.SessionDir)
	sess, err := loadOrCreateSession(store, resumeFlag, cwd, cfg)
	if err != nil {
		return err
	}
	if strings.TrimSpace(imageFlag) != "" && strings.TrimSpace(promptFlag) == "" {
		return fmt.Errorf("-image requires -prompt; use @path/to/image.png in the interactive prompt")
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
		hookOverrides:  NewHookOverrideStore(),
		checkpoints:    NewCheckpointManager(),
		autoCP:         &AutoCheckpointController{},
		verifyHistory:  NewVerificationHistoryStore(),
		interactive:    promptFlag == "",
	}
	defer rt.closeExtensions()

	if rt.interactive {
		rt.showBanner()
	}

	rt.perms = NewPermissionManager(ParseMode(sess.PermissionMode), rt.confirm)
	rt.backgroundJobs = NewBackgroundJobManager(filepath.Join(cwd, userConfigDirName, "jobs"), sess, store)
	rt.workspace = Workspace{
		BaseRoot:              cwd,
		Root:                  sess.WorkingDir,
		Shell:                 cfg.Shell,
		ShellTimeout:          configShellTimeout(cfg),
		ReadHintSpans:         configReadHintSpans(cfg),
		ReadCacheEntries:      configReadCacheEntries(cfg),
		VerificationToolPaths: buildVerificationToolPaths(cfg),
		Perms:                 rt.perms,
		PrepareEdit:           rt.prepareEdit,
		ReportProgress: func(message string) {
			rt.setThinkingStatus(message)
			rt.printProgressMessage(message)
		},
		CurrentSelection: func() *ViewerSelection {
			return rt.session.CurrentSelection()
		},
		PreviewEdit: rt.previewEdit,
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
	if err := rt.ensureConfigured(); err != nil {
		return err
	}
	cfg = rt.cfg
	client, clientErr := NewProviderClient(rt.cfg)
	rt.clientErr = clientErr
	rt.agent = &Agent{
		Config:        rt.cfg,
		Client:        client,
		Tools:         buildRegistry(rt.workspace, nil),
		Workspace:     rt.workspace,
		Session:       rt.session,
		Store:         rt.store,
		Memory:        rt.memory,
		LongMem:       rt.longMem,
		Evidence:      rt.evidence,
		VerifyHistory: rt.verifyHistory,
		PromptResolveAutoVerifyFailure: func(report VerificationReport) (AutoVerifyFailureResolution, error) {
			return rt.promptResolveAutoVerifyFailure(report)
		},
		EmitAssistant: func(text string) {
			rt.printAssistantWhileThinking(text)
		},
		EmitAssistantDelta: func(text string) {
			rt.appendAssistantStream(text)
		},
		EmitProgress: func(text string) {
			rt.setThinkingStatus(compactThinkingStatus(rt.cfg, text))
			rt.printProgressMessage(text)
		},
	}
	if rt.cfg.PlanReview != nil {
		if reviewerClient, reviewerErr := createReviewerClient(rt.cfg.PlanReview, rt.cfg); reviewerErr == nil {
			rt.agent.ReviewerClient = reviewerClient
			rt.agent.ReviewerModel = rt.cfg.PlanReview.Model
		}
	}
	rt.reloadExtensions()

	if promptFlag != "" {
		return rt.runSinglePrompt(promptFlag, cliImages)
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
	if wsSelections, loadErr := LoadWorkspaceSelections(sess.WorkingDir); loadErr == nil && len(wsSelections) > 0 {
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
		NewGrepTool(ws),
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
	if mcp != nil {
		items = append(items, mcp.Tools()...)
	}
	return NewToolRegistry(items...)
}

func (rt *runtimeState) runSinglePrompt(prompt string, images []MessageImage) error {
	if rt.clientErr != nil {
		return rt.clientErr
	}
	ctx := context.Background()
	reply, err := rt.runAgentReplyWithImages(ctx, prompt, images)
	if err != nil {
		return err
	}
	rt.printAssistant(reply)
	return nil
}

func (rt *runtimeState) previewEdit(preview EditPreview) (bool, error) {
	if !rt.interactive {
		return true, nil
	}
	if rt.alwaysApprovePreview {
		return true, nil
	}
	var (
		openPreview bool
		err         error
	)
	rt.withRequestCancelSuspended(func() {
		openPreview, err = rt.confirm("Open diff preview?")
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

func (rt *runtimeState) openEditPreview(preview EditPreview) (bool, error) {
	ok, err := OpenDiffPreview(preview)
	if err == nil {
		return ok, nil
	}
	fmt.Fprintln(rt.writer, rt.ui.warnLine("Falling back to terminal diff preview: "+err.Error()))
	fmt.Fprintln(rt.writer, rt.ui.section("Diff Preview"))
	if strings.TrimSpace(preview.Title) != "" {
		fmt.Fprintln(rt.writer, rt.ui.infoLine(preview.Title))
	}
	fmt.Fprintln(rt.writer, preview.Preview)
	return true, nil
}

func (rt *runtimeState) showBanner() {
	if rt.bannerShown {
		return
	}
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

	for {
		nextTurn := rt.promptTurn + 1
		rt.printTurnSeparator(nextTurn)
		input, err := rt.readInput(rt.ui.prompt(rt.session.Provider, rt.session.Model))
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
		rt.promptTurn = nextTurn
		rt.rememberInputHistory(input)
		if strings.HasPrefix(line, "!") {
			if err := rt.runShell(strings.TrimPrefix(line, "!")); err != nil {
				fmt.Fprintln(rt.writer, rt.ui.errorLine("shell error: "+err.Error()))
			}
			continue
		}
		if cmd, ok := ParseCommand(line); ok {
			exit, err := rt.handleCommand(cmd)
			if err != nil {
				fmt.Fprintln(rt.writer, rt.ui.errorLine("command error: "+err.Error()))
			}
			if exit {
				return nil
			}
			continue
		}
		if rt.clientErr != nil {
			fmt.Fprintln(rt.writer, rt.ui.errorLine("provider error: "+rt.clientErr.Error()))
			if isMissingKeyError(rt.clientErr) {
				_ = rt.handleAuthError()
			}
			continue
		}
		ctx := context.Background()
		reply, err := rt.runAgentReply(ctx, input)
		if err != nil {
			if errors.Is(err, ErrEditCanceled) {
				fmt.Fprintln(rt.writer, rt.ui.infoLine("Edit canceled."))
				continue
			}
			if errors.Is(err, ErrWriteDenied) {
				fmt.Fprintln(rt.writer, rt.ui.infoLine("Write canceled."))
				continue
			}
			if errors.Is(err, ErrInvalidEditPayload) {
				fmt.Fprintln(rt.writer, rt.ui.warnLine(err.Error()))
				continue
			}
			for _, line := range rt.formatAssistantError(err) {
				fmt.Fprintln(rt.writer, line)
			}
			if isAuthError(err) {
				_ = rt.handleAuthError()
			}
			continue
		}
		if strings.TrimSpace(reply) != "" {
			rt.printAssistant(reply)
		}
	}
}

func (rt *runtimeState) runAgentReply(ctx context.Context, input string) (string, error) {
	return rt.runAgentReplyWithImages(ctx, input, nil)
}

func (rt *runtimeState) printTurnSeparator(turn int) {
	fmt.Fprintln(rt.writer)
	fmt.Fprintln(rt.writer, rt.ui.turnSeparator(turn, rt.session.Provider, rt.session.Model))
}

func (rt *runtimeState) formatAssistantError(err error) []string {
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
	if verdict, err := rt.runHook(ctx, HookUserPromptSubmit, HookPayload{
		"user_text": input,
	}); err != nil {
		return "", err
	} else if len(verdict.ContextAdds) > 0 {
		input = strings.TrimSpace(input) + "\n\nAdditional hook guidance:\n- " + strings.Join(verdict.ContextAdds, "\n- ")
	}
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	rt.resetAssistantDedup()
	rt.resetAssistantStream()
	rt.allowThinkingIndicator()
	rt.clearThinkingStatus()
	rt.clearRequestCancelState()
	rt.armAutoCheckpoint()
	defer rt.clearAutoCheckpoint()
	defer rt.allowThinkingIndicator()
	defer rt.clearThinkingStatus()
	defer rt.clearRequestCancelState()

	cancelRequest := func() {
		rt.beginRequestCancel()
		cancel()
	}
	stopEscapeWatcher := startEscapeWatcher(cancelRequest, rt.shouldHonorRequestCancel, rt.confirmRequestCancel)
	defer stopEscapeWatcher()

	rt.startThinkingIndicator()
	defer rt.stopThinkingIndicator()
	defer rt.finishAssistantStream()

	reply, err := rt.agent.ReplyWithImages(requestCtx, input, images)
	if err != nil {
		if requestCtx.Err() == context.Canceled && ctx.Err() == nil {
			rt.noteRecentRequestCancel()
			return "", fmt.Errorf("request canceled by user")
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
	rt.thinkingMu.Unlock()

	go func() {
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()

		lastWidth := 0
		lastRendered := ""
		startedAt := time.Now()
		render := func(frame string) {
			elapsed := time.Since(startedAt)
			status := rt.currentThinkingStatus(elapsed)
			renderFrame := frame
			if !shouldAnimateThinkingStatus(status) {
				renderFrame = "-"
			}
			line := rt.ui.thinkingLine(renderFrame, elapsed, status)
			if line == lastRendered {
				return
			}
			clear := "\r" + strings.Repeat(" ", lastWidth) + "\r"
			rt.writeOutput(clear + line)
			lastWidth = visibleLen(line)
			lastRendered = line
		}

		index := 0
		render(frames[index])
		for {
			select {
			case <-stop:
				clear := "\r" + strings.Repeat(" ", lastWidth) + "\r"
				rt.writeOutput(clear)
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

func (rt *runtimeState) printWhileThinking(lines ...string) {
	rt.flushAssistantStream()
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

func (rt *runtimeState) printProgressMessage(text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	rt.printWhileThinking(rt.ui.activityLine(classifyProgressKind(text), text))
}

func (rt *runtimeState) writeOutput(text string) {
	rt.outputMu.Lock()
	defer rt.outputMu.Unlock()
	fmt.Fprint(rt.writer, text)
}

func (rt *runtimeState) setThinkingStatus(text string) {
	rt.thinkingStatusMu.Lock()
	rt.thinkingStatusOverride = strings.TrimSpace(text)
	rt.thinkingStatusMu.Unlock()
}

func (rt *runtimeState) clearThinkingStatus() {
	rt.thinkingStatusMu.Lock()
	rt.thinkingStatusOverride = ""
	rt.thinkingStatusMu.Unlock()
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
	case strings.Contains(lower, "automatic verification failed"):
		return localizedText(cfg, "Verification failed.", "검증 실패.")
	case strings.Contains(lower, "automatic verification finished"):
		return localizedText(cfg, "Verification finished.", "검증 완료.")
	case strings.Contains(lower, "waiting for the model to summarize"):
		return localizedText(cfg, "Finalizing reply...", "답변 정리 중 ...")
	}

	if len(trimmed) > 48 {
		return trimmed[:45] + "..."
	}
	return trimmed
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
	rt.clearThinkingStatus()
	rt.printWhileThinking(rt.ui.activityLine("next", text))
}

func (rt *runtimeState) shouldHonorRequestCancel() bool {
	rt.requestCancelMu.Lock()
	defer rt.requestCancelMu.Unlock()
	return rt.requestCancelPauses == 0
}

func (rt *runtimeState) confirmRequestCancel() bool {
	if !rt.interactive {
		return true
	}
	var (
		allowed bool
		err     error
	)
	rt.withRequestCancelSuspended(func() {
		allowed, err = rt.confirm("Cancel current request?")
	})
	if err != nil {
		rt.printWhileThinking(rt.ui.warnLine("request cancel confirmation failed: " + err.Error()))
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
	resolved, err := rt.workspace.Resolve(target)
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
	rt.agent.Session.WorkingDir = resolved
	rt.reloadExtensions()
	rt.agent.Workspace = rt.workspace
	rt.memory, _ = LoadMemory(resolved, rt.cfg.MemoryFiles)
	rt.agent.Memory = rt.memory
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
	resolved, err := rt.workspace.Resolve(target)
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

func (rt *runtimeState) confirm(question string) (bool, error) {
	if !rt.interactive {
		return false, fmt.Errorf("interactive confirmation unavailable")
	}
	if rt.autoApproveConfirmation(question) {
		return true, nil
	}
	resumeThinking := rt.suspendThinkingIndicator()
	defer resumeThinking()
	for {
		label := rt.confirmLabel(question)
		answer, usedInteractive, err := rt.readInteractiveLine(label+" ", "", nil, true)
		if !usedInteractive {
			fmt.Fprint(rt.writer, label+" ")
			answer, err = rt.reader.ReadString('\n')
			if err != nil {
				return false, err
			}
			answer = strings.TrimSpace(answer)
		} else if err != nil {
			if errors.Is(err, ErrPromptCanceled) {
				return false, nil
			}
			return false, err
		}
		allowed, always, handled := parseConfirmationAnswer(answer)
		if !handled {
			continue
		}
		if allowed && always {
			rt.rememberConfirmationApproval(question)
		}
		return allowed, nil
	}
}

func (rt *runtimeState) prepareAnalysisDirectorySelection(cfg ProjectAnalysisConfig) (ProjectAnalysisConfig, error) {
	candidates, err := findAnalysisDirectoryCandidates(rt.workspace.Root, cfg)
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
	if isGitApprovalQuestion(question) {
		return rt.perms != nil && rt.perms.IsGitAllowed()
	}
	return false
}

func (rt *runtimeState) confirmLabel(question string) string {
	hint := "[y/N, Esc=cancel]"
	if isDiffPreviewQuestion(question) {
		hint = "[y/N/a=auto-accept, Esc=cancel]"
	} else if supportsAlwaysApproval(question) {
		hint = "[y/N/a=always, Esc=cancel]"
	}
	return rt.ui.warnLine(question) + " " + rt.ui.dim(hint)
}

func supportsAlwaysApproval(question string) bool {
	return isWriteApprovalQuestion(question) || isDiffPreviewQuestion(question) || isGitApprovalQuestion(question)
}

func isWriteApprovalQuestion(question string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(question)), "allow write?")
}

func isDiffPreviewQuestion(question string) bool {
	return strings.EqualFold(strings.TrimSpace(question), "Open diff preview?")
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
	line, usedInteractive, err := rt.readInteractiveLine(label+" ", "", nil, true)
	if !usedInteractive {
		fmt.Fprint(rt.writer, label+" ")
		line, err = rt.reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		line = strings.TrimSpace(line)
	} else if err != nil {
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

func (rt *runtimeState) promptProviderChoice() (string, error) {
	options := []string{
		"1. anthropic",
		"2. openai",
		"3. openrouter",
		"4. ollama",
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Providers"))
	for _, option := range options {
		fmt.Fprintln(rt.writer, rt.ui.info("  "+option))
	}
	defaultChoice := "4"
	switch strings.ToLower(strings.TrimSpace(rt.cfg.Provider)) {
	case "anthropic":
		defaultChoice = "1"
	case "openai", "openai-compatible":
		defaultChoice = "2"
	case "openrouter":
		defaultChoice = "3"
	case "ollama":
		defaultChoice = "4"
	}
	for {
		choice, err := rt.promptValue("Provider number or name", defaultChoice)
		if err != nil {
			return "", err
		}
		switch strings.ToLower(strings.TrimSpace(choice)) {
		case "1", "anthropic":
			return "anthropic", nil
		case "2", "openai", "openai-compatible":
			return "openai", nil
		case "3", "openrouter":
			return "openrouter", nil
		case "4", "ollama":
			return "ollama", nil
		}
		fmt.Fprintln(rt.writer, rt.ui.warnLine("Choose 1, 2, 3, 4, anthropic, openai, openrouter, or ollama."))
	}
}

func (rt *runtimeState) configureProviderInteractive(provider string) error {
	nextProvider := provider
	nextModel := rt.cfg.Model
	nextBaseURL := rt.cfg.BaseURL
	nextAPIKey := rt.storedProviderKey(provider)
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
	case "anthropic", "openai", "openrouter":
		baseURL := ""
		if provider == "openrouter" {
			baseURL = normalizeOpenRouterBaseURL("")
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
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Saved provider=%s model=%s", rt.cfg.Provider, rt.cfg.Model)))
	return nil
}

func (rt *runtimeState) storedProviderKey(provider string) string {
	if rt.cfg.ProviderKeys == nil {
		return ""
	}
	return rt.cfg.ProviderKeys[strings.ToLower(strings.TrimSpace(provider))]
}

func (rt *runtimeState) storeProviderKey(provider, key string) {
	if strings.TrimSpace(key) == "" {
		return
	}
	if rt.cfg.ProviderKeys == nil {
		rt.cfg.ProviderKeys = make(map[string]string)
	}
	rt.cfg.ProviderKeys[strings.ToLower(strings.TrimSpace(provider))] = strings.TrimSpace(key)
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

func providerDisplayName(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "anthropic":
		return "Anthropic"
	case "openai", "openai-compatible":
		return "OpenAI"
	case "openrouter":
		return "OpenRouter"
	case "ollama":
		return "Ollama"
	default:
		return provider
	}
}

func (rt *runtimeState) handleProfileCommand() error {
	if len(rt.cfg.Profiles) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No saved profiles yet."))
		return nil
	}

	rt.cfg.Profiles = normalizedProfileOrder(rt.cfg.Profiles)
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
		if strings.EqualFold(profile.Provider, rt.session.Provider) &&
			strings.EqualFold(profile.Model, rt.session.Model) &&
			strings.EqualFold(strings.TrimSpace(profile.BaseURL), strings.TrimSpace(rt.session.BaseURL)) {
			label += " " + rt.ui.success("[current]")
		}
		if len(meta) > 0 {
			label += "  " + rt.ui.dim(strings.Join(meta, " | "))
		}
		fmt.Fprintln(rt.writer, label)
	}
	fmt.Fprintln(rt.writer)
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Enter a number to activate a profile."))
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Use r<number> to rename, d<number> to delete, p<number> to pin or unpin."))

	choice, err := rt.promptValue("Profile action", "1")
	if err != nil {
		if errors.Is(err, ErrPromptCanceled) {
			return nil
		}
		return err
	}

	action, index, err := parseProfileAction(strings.TrimSpace(choice), len(rt.cfg.Profiles))
	if err != nil {
		return err
	}
	selected := rt.cfg.Profiles[index-1]

	switch action {
	case "activate":
		if strings.TrimSpace(selected.APIKey) != "" {
			rt.cfg.APIKey = selected.APIKey
		} else if requiresAPIKey(selected.Provider) && strings.TrimSpace(rt.cfg.APIKey) == "" {
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

		if err := rt.activateProvider(selected.Provider, selected.Model, selected.BaseURL); err != nil {
			return err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Activated profile "+selected.Name))
		return nil
	case "rename":
		newName, err := rt.promptRequiredValue("New profile name", selected.Name)
		if err != nil {
			if errors.Is(err, ErrPromptCanceled) {
				return nil
			}
			return err
		}
		rt.cfg.Profiles[index-1].Name = newName
		if err := rt.saveUserConfig(); err != nil {
			return err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Renamed profile to "+newName))
		return nil
	case "delete":
		rt.cfg.Profiles = append(rt.cfg.Profiles[:index-1], rt.cfg.Profiles[index:]...)
		if err := rt.saveUserConfig(); err != nil {
			return err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Deleted profile "+selected.Name))
		return nil
	case "pin":
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
	for _, profile := range rt.cfg.Profiles {
		if strings.EqualFold(profile.Provider, rt.session.Provider) &&
			strings.EqualFold(profile.Model, rt.session.Model) &&
			strings.EqualFold(strings.TrimSpace(profile.BaseURL), strings.TrimSpace(rt.session.BaseURL)) {
			return profile, true
		}
	}
	return Profile{}, false
}

func requiresAPIKey(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "anthropic", "openai", "openai-compatible", "openrouter":
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

func (rt *runtimeState) syncClientFromConfig() {
	client, clientErr := NewProviderClient(rt.cfg)
	if rt.agent != nil {
		rt.agent.Config = rt.cfg
		rt.agent.Client = client
	}
	rt.workspace.VerificationToolPaths = buildVerificationToolPaths(rt.cfg)
	rt.clientErr = clientErr
}

func (rt *runtimeState) saveUserConfig() error {
	rt.cfg.Provider = rt.session.Provider
	rt.cfg.Model = rt.session.Model
	rt.cfg.BaseURL = rt.session.BaseURL
	rt.cfg.PermissionMode = rt.session.PermissionMode
	return SaveUserConfig(rt.cfg)
}

func (rt *runtimeState) rememberCurrentProfile() {
	if strings.TrimSpace(rt.session.Provider) == "" || strings.TrimSpace(rt.session.Model) == "" {
		return
	}
	profile := Profile{
		Name:     profileName(rt.session.Provider, rt.session.Model),
		Provider: rt.session.Provider,
		Model:    rt.session.Model,
		BaseURL:  rt.session.BaseURL,
		APIKey:   rt.cfg.APIKey,
	}

	for _, existing := range rt.cfg.Profiles {
		if strings.EqualFold(existing.Provider, profile.Provider) &&
			strings.EqualFold(existing.Model, profile.Model) &&
			strings.EqualFold(strings.TrimSpace(existing.BaseURL), strings.TrimSpace(profile.BaseURL)) {
			if strings.TrimSpace(existing.Name) != "" {
				profile.Name = existing.Name
			}
			profile.Pinned = existing.Pinned
			break
		}
	}

	pinned := make([]Profile, 0, len(rt.cfg.Profiles)+1)
	others := make([]Profile, 0, len(rt.cfg.Profiles)+1)
	for _, existing := range rt.cfg.Profiles {
		if strings.EqualFold(existing.Provider, profile.Provider) &&
			strings.EqualFold(existing.Model, profile.Model) &&
			strings.EqualFold(strings.TrimSpace(existing.BaseURL), strings.TrimSpace(profile.BaseURL)) {
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
	rt.cfg.Profiles = limitProfiles(append(pinned, others...), 5)
}

func (rt *runtimeState) activateProvider(providerName, model, baseURL string) error {
	rt.cfg.Provider = providerName
	rt.cfg.Model = model
	rt.cfg.BaseURL = baseURL
	rt.session.Provider = providerName
	rt.session.Model = model
	rt.session.BaseURL = baseURL
	rt.syncClientFromConfig()
	rt.rememberCurrentProfile()
	if err := rt.store.Save(rt.session); err != nil {
		return err
	}
	if err := rt.saveUserConfig(); err != nil {
		return err
	}
	return rt.clientErr
}

func (rt *runtimeState) handleProviderCommand(args string) error {
	parts := strings.Fields(args)
	if len(parts) > 0 && strings.EqualFold(parts[0], "status") {
		return rt.showProviderStatus()
	}
	if len(parts) > 0 {
		var err error
		switch strings.ToLower(parts[0]) {
		case "1", "anthropic":
			err = rt.configureProviderInteractive("anthropic")
		case "2", "openai", "openai-compatible":
			err = rt.configureProviderInteractive("openai")
		case "3", "openrouter":
			err = rt.configureProviderInteractive("openrouter")
		case "4", "ollama":
			err = rt.configureProviderInteractive("ollama")
		default:
			return fmt.Errorf("unknown provider: %s", parts[0])
		}
		if errors.Is(err, ErrPromptCanceled) {
			return nil
		}
		return err
	}

	fmt.Fprintln(rt.writer, rt.ui.section("Providers"))
	fmt.Fprintln(rt.writer, rt.ui.info("  1. anthropic"))
	fmt.Fprintln(rt.writer, rt.ui.info("  2. openai"))
	fmt.Fprintln(rt.writer, rt.ui.info("  3. openrouter"))
	fmt.Fprintln(rt.writer, rt.ui.info("  4. ollama"))
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Current provider: "+valueOrUnset(rt.session.Provider)))
	defaultChoice := "4"
	switch strings.ToLower(strings.TrimSpace(rt.session.Provider)) {
	case "anthropic":
		defaultChoice = "1"
	case "openai", "openai-compatible":
		defaultChoice = "2"
	case "openrouter":
		defaultChoice = "3"
	case "ollama":
		defaultChoice = "4"
	}
	choice, err := rt.promptValue("Select provider", defaultChoice)
	if err != nil {
		if errors.Is(err, ErrPromptCanceled) {
			return nil
		}
		return err
	}
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "", "1", "anthropic":
		err = rt.configureProviderInteractive("anthropic")
	case "2", "openai", "openai-compatible":
		err = rt.configureProviderInteractive("openai")
	case "3", "openrouter":
		err = rt.configureProviderInteractive("openrouter")
	case "4", "ollama":
		err = rt.configureProviderInteractive("ollama")
	default:
		return fmt.Errorf("unknown provider: %s", choice)
	}
	if errors.Is(err, ErrPromptCanceled) {
		return nil
	}
	return err
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
	fmt.Fprintln(rt.writer, rt.ui.statusKV("api_key", providerAPIKeyState(apiKey)))
	if strings.EqualFold(provider, "ollama") {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("cached_models", fmt.Sprintf("%d", len(rt.ollamaModels))))
	}
	if strings.TrimSpace(provider) != "" {
		rt.printProviderBudgetStatus(provider, base, apiKey)
	}
	return nil
}

func (rt *runtimeState) currentProviderStatus() (string, string, string, string) {
	provider := strings.ToLower(firstNonEmptyTrimmed(rt.session.Provider, rt.cfg.Provider))
	model := firstNonEmptyTrimmed(rt.session.Model, rt.cfg.Model)
	baseURL := normalizeProviderBaseURL(provider, firstNonEmptyTrimmed(rt.session.BaseURL, rt.cfg.BaseURL))
	apiKey := strings.TrimSpace(rt.storedProviderKey(provider))
	if strings.EqualFold(provider, rt.cfg.Provider) && strings.TrimSpace(rt.cfg.APIKey) != "" {
		apiKey = strings.TrimSpace(rt.cfg.APIKey)
	}
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
	case "openrouter":
		rt.printOpenRouterBudgetStatus(baseURL, apiKey)
	case "ollama":
		fmt.Fprintln(rt.writer, rt.ui.statusKV("remaining_balance", "Not applicable for local providers."))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("usage_cost_api", "No remote billing API is expected for local model servers."))
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

func (rt *runtimeState) handleCommand(cmd Command) (bool, error) {
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
			kv("permission_mode", valueOrUnset(rt.session.PermissionMode)),
		)
		fmt.Fprintln(rt.writer)
		rt.printKVGroup("Workspace",
			kv("cwd", rt.session.WorkingDir),
			kv("session", rt.session.ID),
			kv("sessions_dir", rt.store.Root()),
		)
		if strings.TrimSpace(rt.session.ActiveFeatureID) != "" {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("active_feature", rt.session.ActiveFeatureID))
		}
		if rt.session.LastSelection != nil && rt.session.LastSelection.HasSelection() {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("selection", rt.session.LastSelection.Summary(rt.workspace.Root)))
		}
		fmt.Fprintln(rt.writer)
		rt.printKVGroup("Data",
			kv("memory_files", fmt.Sprintf("%d", len(rt.memory.Files))),
			kv("persistent_memory", fmt.Sprintf("%d", rt.persistentMemoryCount())),
			kv("evidence_records", fmt.Sprintf("%d", rt.evidenceCount())),
		)
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
		if cmd.Args == "" {
			fmt.Fprintln(rt.writer, rt.ui.infoLine("Current model: "+rt.session.Model))
			return false, nil
		}
		rt.session.Model = cmd.Args
		rt.cfg.Model = cmd.Args
		rt.syncClientFromConfig()
		_ = rt.store.Save(rt.session)
		if err := rt.saveUserConfig(); err != nil {
			return false, err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Model set to "+cmd.Args))
	case "permissions":
		if cmd.Args == "" {
			fmt.Fprintln(rt.writer, rt.ui.infoLine("Permissions: "+string(rt.perms.Mode())))
			return false, nil
		}
		mode := ParseMode(cmd.Args)
		rt.perms.SetMode(mode)
		rt.session.PermissionMode = string(mode)
		_ = rt.store.Save(rt.session)
		if err := rt.saveUserConfig(); err != nil {
			return false, err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Permissions set to "+string(mode)))
	case "set-max-tool-iterations":
		if cmd.Args == "" {
			fmt.Fprintln(rt.writer, rt.ui.infoLine("max_tool_iterations: "+fmt.Sprintf("%d", configMaxToolIterations(rt.cfg))))
			return false, nil
		}
		val, err := strconv.Atoi(strings.TrimSpace(cmd.Args))
		if err != nil || val < 1 {
			return false, fmt.Errorf("invalid value: must be a positive integer")
		}
		rt.cfg.MaxToolIterations = val
		if err := rt.saveUserConfig(); err != nil {
			return false, err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("max_tool_iterations set to "+fmt.Sprintf("%d", val)))
	case "verify":
		if err := rt.handleVerifyCommand(cmd.Args); err != nil {
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
		fmt.Fprintln(rt.writer, rt.agent.Compact(cmd.Args))
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
			if strings.TrimSpace(status.Error) != "" {
				fmt.Fprintf(rt.writer, "%s  error=%s\n", status.Name, status.Error)
				continue
			}
			fmt.Fprintf(rt.writer, "%s  tools=%d  resources=%d  prompts=%d  cwd=%s\n", status.Name, status.ToolCount, status.ResourceCount, status.PromptCount, status.Cwd)
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
	case "review-selection":
		if err := rt.handleSelectionReviewCommand(cmd.Args); err != nil {
			return false, err
		}
	case "review-selections":
		if err := rt.handleSelectionsReviewCommand(cmd.Args); err != nil {
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
		rt.agent.Session = loaded
		rt.cfg.Provider = loaded.Provider
		rt.cfg.Model = loaded.Model
		rt.cfg.BaseURL = loaded.BaseURL
		rt.workspace.Root = loaded.WorkingDir
		rt.memory, _ = LoadMemory(loaded.WorkingDir, rt.cfg.MemoryFiles)
		rt.agent.Memory = rt.memory
		rt.reloadExtensions()
		rt.agent.Workspace = rt.workspace
		rt.syncClientFromConfig()
		rt.perms.SetMode(ParseMode(loaded.PermissionMode))
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
	case "tasks":
		if len(rt.session.Plan) == 0 {
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
		return false, rt.handleProfileCommand()
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
		fmt.Fprintln(rt.writer, rt.ui.hintLine("Effective settings merged from config files, environment, and session overrides."))
		fmt.Fprintln(rt.writer)
		rt.printKVGroup("Model",
			kv("provider", valueOrUnset(rt.cfg.Provider)),
			kv("model", valueOrUnset(rt.cfg.Model)),
			kv("max_tokens", fmt.Sprintf("%d", rt.cfg.MaxTokens)),
			kv("base_url", valueOrUnset(rt.cfg.BaseURL)),
		)
		fmt.Fprintln(rt.writer)
		rt.printKVGroup("Runtime",
			kv("shell", valueOrUnset(rt.cfg.Shell)),
			kv("permission_mode", string(rt.perms.Mode())),
			kv("session_dir", rt.cfg.SessionDir),
			kv("max_tool_iterations", fmt.Sprintf("%d", configMaxToolIterations(rt.cfg))),
			kv("auto_compact_chars", fmt.Sprintf("%d", rt.cfg.AutoCompactChars)),
		)
		fmt.Fprintln(rt.writer)
		rt.printKVGroup("Automation",
			kv("auto_checkpoint_edits", fmt.Sprintf("%t", configAutoCheckpointEdits(rt.cfg))),
			kv("auto_verify", fmt.Sprintf("%t", configAutoVerify(rt.cfg))),
			kv("auto_locale", fmt.Sprintf("%t", configAutoLocale(rt.cfg))),
		)
		fmt.Fprintln(rt.writer)
		rt.printKVGroup("Hooks",
			kv("hooks_enabled", fmt.Sprintf("%t", configHooksEnabled(rt.cfg))),
			kv("hook_presets", fmt.Sprintf("%d", len(rt.cfg.HookPresets))),
			kv("hook_rules", fmt.Sprintf("%d", rt.hookRuleCount())),
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
		if rt.longMem != nil {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("persistent_memory_path", rt.longMem.Path))
		}
		if rt.checkpoints != nil {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("checkpoint_root", rt.checkpoints.Root))
		}
	case "set-plan-review":
		if err := rt.handleSetPlanReviewCommand(cmd.Args); err != nil {
			return false, err
		}
	case "set-analysis-models":
		if err := rt.handleSetAnalysisModelsCommand(cmd.Args); err != nil {
			return false, err
		}
	case "do-plan-review":
		if err := rt.handleDoPlanReviewCommand(cmd.Args); err != nil {
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
	case "analyze-performance":
		if err := rt.handleAnalyzePerformanceCommand(cmd.Args); err != nil {
			return false, err
		}
	case "profile-review":
		return false, rt.handleProfileReviewCommand()
	case "exit", "quit":
		return true, nil
	default:
		return false, fmt.Errorf("unknown command: /%s", cmd.Name)
	}
	return false, nil
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
	if err := SaveUserConfig(rt.cfg); err != nil {
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
	resumeThinking := rt.suspendThinkingIndicator()
	defer resumeThinking()

	toolName := report.MissingCommandTool()
	label := rt.ui.warnLine("Automatic verification failed because a verification tool could not be started.") + " " + rt.ui.dim("[p=set path/y=disable now/a=always disable for this workspace, Esc=cancel]")
	for {
		answer, usedInteractive, err := rt.readInteractiveLine(label+" ", "", nil, true)
		if !usedInteractive {
			fmt.Fprint(rt.writer, label+" ")
			answer, err = rt.reader.ReadString('\n')
			if err != nil {
				return AutoVerifyFailureNoAction, err
			}
			answer = strings.TrimSpace(answer)
		} else if err != nil {
			if errors.Is(err, ErrPromptCanceled) {
				return AutoVerifyFailureNoAction, nil
			}
			return AutoVerifyFailureNoAction, err
		}

		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "p", "path":
			if strings.TrimSpace(toolName) == "" {
				fmt.Fprintln(rt.writer, rt.ui.warnLine("Could not identify which verification tool is missing."))
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
			applyVerificationToolPathToConfig(&rt.cfg, toolName, resolvedPath)
			rt.workspace.VerificationToolPaths = buildVerificationToolPaths(rt.cfg)
			if rt.agent != nil {
				rt.agent.Config = rt.cfg
			}
			fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("%s path saved for this workspace.", strings.ToUpper(toolName))))
			return AutoVerifyFailureRetry, nil
		case "y", "yes":
			rt.cfg.AutoVerify = boolPtr(false)
			if rt.agent != nil {
				rt.agent.Config = rt.cfg
			}
			fmt.Fprintln(rt.writer, rt.ui.successLine("Automatic verification disabled for this session."))
			if summary := strings.TrimSpace(report.FailureSummary()); summary != "" {
				fmt.Fprintln(rt.writer, rt.ui.warnLine("Latest verification failure:"))
				fmt.Fprintln(rt.writer, rt.ui.dim(summary))
			}
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
			fmt.Fprintln(rt.writer, rt.ui.successLine("Automatic verification disabled for this workspace."))
			if summary := strings.TrimSpace(report.FailureSummary()); summary != "" {
				fmt.Fprintln(rt.writer, rt.ui.warnLine("Latest verification failure:"))
				fmt.Fprintln(rt.writer, rt.ui.dim(summary))
			}
			return AutoVerifyFailureDisable, nil
		case "", "n", "no":
			return AutoVerifyFailureNoAction, nil
		}
	}
}

func (rt *runtimeState) handleSetPlanReviewCommand(args string) error {
	parts := strings.Fields(args)

	// Show current config if no args and no interactive selection
	if len(parts) > 0 {
		var err error
		switch strings.ToLower(parts[0]) {
		case "status":
			return rt.showPlanReviewStatus()
		case "1", "anthropic":
			err = rt.configurePlanReviewInteractive("anthropic")
		case "2", "openai", "openai-compatible":
			err = rt.configurePlanReviewInteractive("openai")
		case "3", "openrouter":
			err = rt.configurePlanReviewInteractive("openrouter")
		case "4", "ollama":
			err = rt.configurePlanReviewInteractive("ollama")
		default:
			return fmt.Errorf("unknown provider: %s", parts[0])
		}
		if errors.Is(err, ErrPromptCanceled) {
			return nil
		}
		return err
	}

	fmt.Fprintln(rt.writer, rt.ui.section("Set Plan Review Reviewer"))
	if rt.cfg.PlanReview != nil {
		fmt.Fprintln(rt.writer, rt.ui.hintLine("Current reviewer: "+rt.cfg.PlanReview.Provider+" / "+rt.cfg.PlanReview.Model))
	} else {
		fmt.Fprintln(rt.writer, rt.ui.hintLine("No reviewer configured yet."))
	}
	fmt.Fprintln(rt.writer, rt.ui.info("  1. anthropic"))
	fmt.Fprintln(rt.writer, rt.ui.info("  2. openai"))
	fmt.Fprintln(rt.writer, rt.ui.info("  3. openrouter"))
	fmt.Fprintln(rt.writer, rt.ui.info("  4. ollama"))

	defaultChoice := "1"
	if rt.cfg.PlanReview != nil {
		switch strings.ToLower(strings.TrimSpace(rt.cfg.PlanReview.Provider)) {
		case "anthropic":
			defaultChoice = "1"
		case "openai", "openai-compatible":
			defaultChoice = "2"
		case "openrouter":
			defaultChoice = "3"
		case "ollama":
			defaultChoice = "4"
		}
	}
	choice, err := rt.promptValue("Select provider", defaultChoice)
	if err != nil {
		if errors.Is(err, ErrPromptCanceled) {
			return nil
		}
		return err
	}
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "", "1", "anthropic":
		err = rt.configurePlanReviewInteractive("anthropic")
	case "2", "openai", "openai-compatible":
		err = rt.configurePlanReviewInteractive("openai")
	case "3", "openrouter":
		err = rt.configurePlanReviewInteractive("openrouter")
	case "4", "ollama":
		err = rt.configurePlanReviewInteractive("ollama")
	default:
		return fmt.Errorf("unknown provider: %s", choice)
	}
	if errors.Is(err, ErrPromptCanceled) {
		return nil
	}
	return err
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
			if err := SaveUserConfig(rt.cfg); err != nil {
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
		fmt.Fprintln(rt.writer, rt.ui.statusKV("worker", rt.cfg.Provider+" / "+rt.cfg.Model+" (inherits main model)"))
	} else {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("worker", rt.cfg.ProjectAnalysis.WorkerProfile.Provider+" / "+rt.cfg.ProjectAnalysis.WorkerProfile.Model))
	}
	if rt.cfg.ProjectAnalysis.ReviewerProfile == nil {
		if rt.cfg.ProjectAnalysis.WorkerProfile != nil {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("reviewer", rt.cfg.ProjectAnalysis.WorkerProfile.Provider+" / "+rt.cfg.ProjectAnalysis.WorkerProfile.Model+" (inherits worker model)"))
		} else {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("reviewer", rt.cfg.Provider+" / "+rt.cfg.Model+" (inherits main model)"))
		}
	} else {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("reviewer", rt.cfg.ProjectAnalysis.ReviewerProfile.Provider+" / "+rt.cfg.ProjectAnalysis.ReviewerProfile.Model))
	}
	incrementalEnabled := rt.cfg.ProjectAnalysis.Incremental == nil || *rt.cfg.ProjectAnalysis.Incremental
	fmt.Fprintln(rt.writer, rt.ui.statusKV("incremental", fmt.Sprintf("%t", incrementalEnabled)))
	return nil
}

func (rt *runtimeState) configureProjectAnalysisRoleInteractive(role string, providerArg string) error {
	provider := strings.ToLower(strings.TrimSpace(providerArg))
	if provider == "" {
		fmt.Fprintln(rt.writer, rt.ui.section("Set Analysis "+strings.Title(role)))
		fmt.Fprintln(rt.writer, rt.ui.info("  1. anthropic"))
		fmt.Fprintln(rt.writer, rt.ui.info("  2. openai"))
		fmt.Fprintln(rt.writer, rt.ui.info("  3. openrouter"))
		fmt.Fprintln(rt.writer, rt.ui.info("  4. ollama"))
		defaultChoice := "1"
		current := rt.cfg.ProjectAnalysis.WorkerProfile
		if role == "reviewer" {
			current = rt.cfg.ProjectAnalysis.ReviewerProfile
		}
		if current != nil {
			switch strings.ToLower(strings.TrimSpace(current.Provider)) {
			case "anthropic":
				defaultChoice = "1"
			case "openai", "openai-compatible":
				defaultChoice = "2"
			case "openrouter":
				defaultChoice = "3"
			case "ollama":
				defaultChoice = "4"
			}
		}
		choice, err := rt.promptValue("Select provider", defaultChoice)
		if err != nil {
			return err
		}
		switch strings.ToLower(strings.TrimSpace(choice)) {
		case "", "1", "anthropic":
			provider = "anthropic"
		case "2", "openai", "openai-compatible":
			provider = "openai"
		case "3", "openrouter":
			provider = "openrouter"
		case "4", "ollama":
			provider = "ollama"
		default:
			return fmt.Errorf("unknown provider: %s", choice)
		}
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
	case "anthropic", "openai", "openrouter":
		if strings.TrimSpace(nextAPIKey) == "" && strings.EqualFold(provider, rt.cfg.Provider) {
			nextAPIKey = rt.cfg.APIKey
		}
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
	default:
		return fmt.Errorf("unsupported provider: %s", provider)
	}

	return rt.activateProjectAnalysisRole(role, provider, nextModel, nextBaseURL, nextAPIKey)
}

func (rt *runtimeState) activateProjectAnalysisRole(role string, provider string, model string, baseURL string, apiKey string) error {
	profile := &Profile{
		Name:     profileName(provider, model),
		Provider: provider,
		Model:    model,
		BaseURL:  normalizeProfileBaseURL(provider, baseURL),
		APIKey:   apiKey,
	}
	if role == "worker" {
		rt.cfg.ProjectAnalysis.WorkerProfile = profile
	} else if role == "reviewer" {
		rt.cfg.ProjectAnalysis.ReviewerProfile = profile
	} else {
		return fmt.Errorf("unsupported analysis role: %s", role)
	}
	if err := SaveUserConfig(rt.cfg); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Analysis %s set: %s / %s", role, provider, model)))
	return nil
}

func (rt *runtimeState) showPlanReviewStatus() error {
	if rt.cfg.PlanReview == nil {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No plan-review reviewer configured."))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Plan Review Reviewer"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("provider", rt.cfg.PlanReview.Provider))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("model", rt.cfg.PlanReview.Model))
	if rt.cfg.PlanReview.BaseURL != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("base_url", rt.cfg.PlanReview.BaseURL))
	}
	return nil
}

func (rt *runtimeState) configurePlanReviewInteractive(provider string) error {
	nextModel := ""
	nextBaseURL := ""
	nextAPIKey := ""

	if rt.cfg.PlanReview != nil && strings.EqualFold(rt.cfg.PlanReview.Provider, provider) {
		nextModel = rt.cfg.PlanReview.Model
		nextBaseURL = rt.cfg.PlanReview.BaseURL
		nextAPIKey = rt.cfg.PlanReview.APIKey
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
	case "anthropic", "openai", "openrouter":
		baseURL := ""
		if provider == "openrouter" {
			baseURL = normalizeOpenRouterBaseURL("")
		}
		nextBaseURL = baseURL

		// Try to inherit API key from main config if same provider
		if strings.TrimSpace(nextAPIKey) == "" && strings.EqualFold(provider, rt.cfg.Provider) {
			nextAPIKey = rt.cfg.APIKey
		}
		if strings.TrimSpace(nextAPIKey) == "" {
			keyPrompt := providerDisplayName(provider) + " API key (for reviewer)"
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

	return rt.activatePlanReview(provider, nextModel, nextBaseURL, nextAPIKey)
}

func (rt *runtimeState) activatePlanReview(provider, model, baseURL, apiKey string) error {
	baseURL = normalizeProfileBaseURL(provider, baseURL)
	rt.cfg.PlanReview = &PlanReviewConfig{
		Provider: provider,
		Model:    model,
		BaseURL:  baseURL,
		APIKey:   apiKey,
	}
	rt.rememberReviewProfile()
	if err := SaveUserConfig(rt.cfg); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Plan review reviewer set: %s / %s", provider, model)))
	return nil
}

func (rt *runtimeState) rememberReviewProfile() {
	if rt.cfg.PlanReview == nil {
		return
	}
	pr := rt.cfg.PlanReview
	if strings.TrimSpace(pr.Provider) == "" || strings.TrimSpace(pr.Model) == "" {
		return
	}
	profile := Profile{
		Name:     profileName(pr.Provider, pr.Model),
		Provider: pr.Provider,
		Model:    pr.Model,
		BaseURL:  pr.BaseURL,
		APIKey:   pr.APIKey,
	}

	for _, existing := range rt.cfg.ReviewProfiles {
		if strings.EqualFold(existing.Provider, profile.Provider) &&
			strings.EqualFold(existing.Model, profile.Model) &&
			strings.EqualFold(strings.TrimSpace(existing.BaseURL), strings.TrimSpace(profile.BaseURL)) {
			if strings.TrimSpace(existing.Name) != "" {
				profile.Name = existing.Name
			}
			profile.Pinned = existing.Pinned
			break
		}
	}

	pinned := make([]Profile, 0, len(rt.cfg.ReviewProfiles)+1)
	others := make([]Profile, 0, len(rt.cfg.ReviewProfiles)+1)
	for _, existing := range rt.cfg.ReviewProfiles {
		if strings.EqualFold(existing.Provider, profile.Provider) &&
			strings.EqualFold(existing.Model, profile.Model) &&
			strings.EqualFold(strings.TrimSpace(existing.BaseURL), strings.TrimSpace(profile.BaseURL)) {
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
	rt.cfg.ReviewProfiles = limitProfiles(append(pinned, others...), 5)
}

func (rt *runtimeState) currentReviewProfile() (Profile, bool) {
	if rt.cfg.PlanReview == nil {
		return Profile{}, false
	}
	for _, profile := range rt.cfg.ReviewProfiles {
		if strings.EqualFold(profile.Provider, rt.cfg.PlanReview.Provider) &&
			strings.EqualFold(profile.Model, rt.cfg.PlanReview.Model) &&
			strings.EqualFold(strings.TrimSpace(profile.BaseURL), strings.TrimSpace(rt.cfg.PlanReview.BaseURL)) {
			return profile, true
		}
	}
	return Profile{}, false
}

func (rt *runtimeState) handleProfileReviewCommand() error {
	if len(rt.cfg.ReviewProfiles) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No saved review profiles yet. Use /set-plan-review to add one."))
		return nil
	}

	rt.cfg.ReviewProfiles = normalizedProfileOrder(rt.cfg.ReviewProfiles)
	fmt.Fprintln(rt.writer, rt.ui.section("Review Profiles"))
	if current, ok := rt.currentReviewProfile(); ok {
		fmt.Fprintln(rt.writer, rt.ui.infoLine("Current reviewer: "+current.Name))
		meta := []string{current.Provider, current.Model}
		if current.BaseURL != "" {
			meta = append(meta, current.BaseURL)
		}
		if current.Pinned {
			meta = append(meta, "pinned")
		}
		fmt.Fprintln(rt.writer, rt.ui.dim(strings.Join(meta, " | ")))
		fmt.Fprintln(rt.writer)
	}
	for i, profile := range rt.cfg.ReviewProfiles {
		label := fmt.Sprintf("%2d. %s", i+1, profile.Name)
		meta := []string{}
		if profile.Pinned {
			meta = append(meta, "pinned")
		}
		if profile.BaseURL != "" {
			meta = append(meta, profile.BaseURL)
		}
		if rt.cfg.PlanReview != nil &&
			strings.EqualFold(profile.Provider, rt.cfg.PlanReview.Provider) &&
			strings.EqualFold(profile.Model, rt.cfg.PlanReview.Model) &&
			strings.EqualFold(strings.TrimSpace(profile.BaseURL), strings.TrimSpace(rt.cfg.PlanReview.BaseURL)) {
			label += " " + rt.ui.success("[current]")
		}
		if len(meta) > 0 {
			label += "  " + rt.ui.dim(strings.Join(meta, " | "))
		}
		fmt.Fprintln(rt.writer, label)
	}
	fmt.Fprintln(rt.writer)
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Enter a number to activate a review profile."))
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Use r<number> to rename, d<number> to delete, p<number> to pin or unpin."))

	choice, err := rt.promptValue("Profile action", "1")
	if err != nil {
		if errors.Is(err, ErrPromptCanceled) {
			return nil
		}
		return err
	}

	action, index, err := parseProfileAction(strings.TrimSpace(choice), len(rt.cfg.ReviewProfiles))
	if err != nil {
		return err
	}
	selected := rt.cfg.ReviewProfiles[index-1]

	switch action {
	case "activate":
		apiKey := selected.APIKey
		if strings.TrimSpace(apiKey) == "" && strings.EqualFold(selected.Provider, rt.cfg.Provider) {
			apiKey = rt.cfg.APIKey
		}
		if strings.TrimSpace(apiKey) == "" && requiresAPIKey(selected.Provider) {
			keyPrompt := providerDisplayName(selected.Provider) + " API key (for reviewer)"
			key, err := rt.promptRequiredValue(keyPrompt, "")
			if err != nil {
				if errors.Is(err, ErrPromptCanceled) {
					return nil
				}
				return err
			}
			apiKey = key
		}
		if err := rt.activatePlanReview(selected.Provider, selected.Model, selected.BaseURL, apiKey); err != nil {
			return err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Activated review profile "+selected.Name))
		return nil
	case "rename":
		newName, err := rt.promptRequiredValue("New profile name", selected.Name)
		if err != nil {
			if errors.Is(err, ErrPromptCanceled) {
				return nil
			}
			return err
		}
		rt.cfg.ReviewProfiles[index-1].Name = newName
		if err := SaveUserConfig(rt.cfg); err != nil {
			return err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Renamed review profile to "+newName))
		return nil
	case "delete":
		rt.cfg.ReviewProfiles = append(rt.cfg.ReviewProfiles[:index-1], rt.cfg.ReviewProfiles[index:]...)
		if err := SaveUserConfig(rt.cfg); err != nil {
			return err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Deleted review profile "+selected.Name))
		return nil
	case "pin":
		newPinned := !rt.cfg.ReviewProfiles[index-1].Pinned
		rt.cfg.ReviewProfiles[index-1].Pinned = newPinned
		rt.cfg.ReviewProfiles = normalizedProfileOrder(rt.cfg.ReviewProfiles)
		if err := SaveUserConfig(rt.cfg); err != nil {
			return err
		}
		state := "unpinned"
		if newPinned {
			state = "pinned"
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine(state+" review profile "+selected.Name))
		return nil
	default:
		return fmt.Errorf("unsupported profile action: %s", action)
	}
}

func (rt *runtimeState) handleDoPlanReviewCommand(args string) error {
	if strings.TrimSpace(args) == "" {
		return fmt.Errorf("usage: /do-plan-review <task description>")
	}
	if rt.agent == nil || rt.agent.Client == nil {
		return fmt.Errorf("no model provider is configured")
	}
	if rt.cfg.PlanReview == nil {
		return fmt.Errorf("no plan-review reviewer configured. Use /set-plan-review <provider> <model> first")
	}

	reviewerClient, err := createReviewerClient(rt.cfg.PlanReview, rt.cfg)
	if err != nil {
		return fmt.Errorf("failed to create reviewer client: %w", err)
	}

	memoryContext := strings.TrimSpace(rt.memory.Combined())

	fmt.Fprintln(rt.writer, rt.ui.section("Plan Review"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("planner", rt.session.Provider+" / "+rt.session.Model))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("reviewer", rt.cfg.PlanReview.Provider+" / "+rt.cfg.PlanReview.Model))
	fmt.Fprintln(rt.writer)

	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stopEscapeWatcher := startEscapeWatcher(cancel, rt.shouldHonorRequestCancel, rt.confirmRequestCancel)
	defer stopEscapeWatcher()

	result, err := RunPlanReview(
		requestCtx,
		rt.agent.Client,
		rt.session.Model,
		reviewerClient,
		rt.cfg.PlanReview.Model,
		rt.appendSimulationPlanningContext(args, args),
		rt.session.WorkingDir,
		memoryContext,
		rt.cfg.MaxTokens,
		rt.cfg.Temperature,
		func(status string) {
			fmt.Fprintln(rt.writer, rt.ui.hintLine(status))
		},
	)
	if err != nil {
		if requestCtx.Err() == context.Canceled {
			rt.noteRecentRequestCancel()
			return fmt.Errorf("plan review canceled by user")
		}
		return err
	}

	// Show review log
	for i, round := range result.ReviewLog {
		fmt.Fprintln(rt.writer, rt.ui.section(fmt.Sprintf("Round %d - Plan", i+1)))
		fmt.Fprintln(rt.writer, round.Plan)
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, rt.ui.section(fmt.Sprintf("Round %d - Review", i+1)))
		fmt.Fprintln(rt.writer, round.Review)
		fmt.Fprintln(rt.writer)
	}

	// Show final plan
	approvedLabel := "not approved (max rounds reached)"
	if result.Approved {
		approvedLabel = "approved"
	}
	fmt.Fprintln(rt.writer, rt.ui.section(fmt.Sprintf("Final Plan (%s, %d rounds)", approvedLabel, result.Rounds)))
	fmt.Fprintln(rt.writer, result.FinalPlan)
	fmt.Fprintln(rt.writer)

	// Ask user whether to proceed
	proceed, err := rt.confirm("Proceed with this plan?")
	if err != nil {
		return err
	}
	if !proceed {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("Plan review aborted by user."))
		return nil
	}

	// Execute the plan via the planner model through the normal agent flow
	executionPrompt := fmt.Sprintf("Execute the following implementation plan. Follow it step by step.\n\n%s", result.FinalPlan)
	executionPrompt = rt.appendSimulationPlanningContext(executionPrompt, args+"\n"+result.FinalPlan)
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Executing plan..."))
	reply, err := rt.runAgentReply(requestCtx, executionPrompt)
	if err != nil {
		return err
	}
	rt.printAssistant(reply)
	return nil
}

func (rt *runtimeState) handleAnalyzeProjectCommand(args string) error {
	mode, goal, err := parseAnalyzeProjectArgs(args)
	if err != nil {
		return err
	}
	if rt.agent == nil || rt.agent.Client == nil {
		return fmt.Errorf("no model provider is configured")
	}

	fmt.Fprintln(rt.writer, rt.ui.section("Project Analysis"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("planner", rt.session.Provider+" / "+rt.session.Model))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("conductor_model", rt.session.Provider+" / "+rt.session.Model))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("workspace", rt.session.WorkingDir))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("analysis_mode", projectAnalysisModeStatus(mode, goal)))
	analysisCfg := configProjectAnalysis(rt.cfg, rt.workspace.BaseRoot)
	analysisCfg, err = rt.prepareAnalysisDirectorySelection(analysisCfg)
	if err != nil {
		return err
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
	fmt.Fprintln(rt.writer, rt.ui.statusKV("analysis_worker", workerLabel))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("analysis_reviewer", reviewerLabel))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("incremental", fmt.Sprintf("%t", incrementalEnabled)))

	analyzer := newProjectAnalyzer(rt.cfg, rt.agent.Client, rt.workspace, func(status string) {
		fmt.Fprintln(rt.writer, rt.ui.hintLine(status))
	}, func(debug string) {
		fmt.Fprintln(rt.writer, rt.ui.infoLine("analysis: "+debug))
	})
	analyzer.analysisCfg = analysisCfg
	previewSnapshot, err := analyzer.scanProject()
	if err != nil {
		return err
	}
	estimatedConcurrency := analyzer.estimateAgentCount(previewSnapshot)
	estimatedTotalShards := analyzer.estimateShardCount(previewSnapshot, estimatedConcurrency)
	plannedShards := analyzer.planShards(previewSnapshot, estimatedTotalShards)
	scope, scopedShards := deriveScopedAnalysisShards(goal, previewSnapshot, plannedShards)
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

	analyzer = newProjectAnalyzer(rt.cfg, rt.agent.Client, rt.workspace, func(status string) {
		rt.printWhileThinking(rt.ui.hintLine(status))
	}, func(debug string) {
		rt.printWhileThinking(rt.ui.infoLine("analysis: " + debug))
	})
	analyzer.analysisCfg = analysisCfg
	run, err := analyzer.Run(requestCtx, goal, mode)
	if err != nil {
		if requestCtx.Err() == context.Canceled {
			rt.noteRecentRequestCancel()
			return fmt.Errorf("project analysis canceled by user")
		}
		return err
	}

	rt.session.LastAnalysis = &run.Summary
	rt.session.Summary = mergeSessionSummaryWithAnalysis(rt.session.Summary, run)
	rt.session.LastAnalysisContextQuery = ""
	rt.session.LastAnalysisContextRunID = run.Summary.RunID
	if err := rt.store.Save(rt.session); err != nil {
		return err
	}

	rt.printWhileThinking(rt.ui.successLine(fmt.Sprintf("Analysis completed with %d shard(s).", run.Summary.TotalShards)))
	if run.Summary.ReviewFailures > 0 {
		rt.printWhileThinking(rt.ui.statusKV("review_failures", fmt.Sprintf("%d", run.Summary.ReviewFailures)))
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
	rt.printWhileThinking(rt.ui.statusKV("cache_reused", fmt.Sprintf("%d", reused)))
	rt.printWhileThinking(rt.ui.statusKV("cache_miss", fmt.Sprintf("%d", missed)))
	if len(missReasons) > 0 {
		reasons := make([]string, 0, len(missReasons))
		for reason, count := range missReasons {
			reasons = append(reasons, fmt.Sprintf("%s=%d", reason, count))
		}
		slices.Sort(reasons)
		rt.printWhileThinking(rt.ui.statusKV("cache_miss_reasons", strings.Join(reasons, ", ")))
	}
	rt.printWhileThinking(rt.ui.statusKV("output", run.Summary.OutputPath))
	rt.printAssistant(run.FinalDocument)
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
		rt.printWhileThinking(rt.ui.statusKV("output", reportPath))
		rt.printWhileThinking(rt.ui.statusKV("output_json", jsonPath))
	}
	rt.printAssistant(reply)
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
	loaded, err := LoadConfig(rt.workspace.BaseRoot)
	if err != nil {
		return err
	}

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
	activePermission := rt.session.PermissionMode
	if strings.TrimSpace(activePermission) == strings.TrimSpace(rt.cfg.PermissionMode) {
		activePermission = loaded.PermissionMode
	}

	rt.cfg = loaded
	rt.session.Provider = activeProvider
	rt.session.Model = activeModel
	rt.session.BaseURL = activeBaseURL
	rt.session.PermissionMode = activePermission
	rt.workspace.Shell = rt.cfg.Shell
	rt.workspace.ShellTimeout = configShellTimeout(rt.cfg)
	rt.workspace.ReadHintSpans = configReadHintSpans(rt.cfg)
	rt.workspace.ReadCacheEntries = configReadCacheEntries(rt.cfg)
	rt.workspace.VerificationToolPaths = buildVerificationToolPaths(rt.cfg)
	rt.perms.SetMode(ParseMode(rt.session.PermissionMode))
	rt.store = NewSessionStore(rt.cfg.SessionDir)

	rt.memory, err = LoadMemory(rt.session.WorkingDir, rt.cfg.MemoryFiles)
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

func (rt *runtimeState) reloadExtensions() {
	rt.skills, rt.skillWarns = LoadSkills(rt.session.WorkingDir, rt.cfg.SkillPaths, rt.cfg.EnabledSkills)
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
			return rt.checkpoints.Create(workspaceSnapshotRoot(rt.workspace), note)
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

func parseAnalyzeProjectArgs(raw string) (string, string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", fmt.Errorf(projectAnalysisUsage())
	}
	fields := strings.Fields(trimmed)
	mode := ""
	goalParts := []string{}
	for index := 0; index < len(fields); index++ {
		field := strings.TrimSpace(fields[index])
		switch {
		case strings.HasPrefix(field, "--mode="):
			value := strings.TrimSpace(strings.TrimPrefix(field, "--mode="))
			mode = normalizeProjectAnalysisMode(value)
			if mode == "" {
				return "", "", fmt.Errorf("invalid analyze-project mode %q; expected one of %s", value, strings.Join(supportedProjectAnalysisModes, ", "))
			}
		case field == "--mode":
			if index+1 >= len(fields) {
				return "", "", fmt.Errorf(projectAnalysisUsage())
			}
			index++
			value := strings.TrimSpace(fields[index])
			mode = normalizeProjectAnalysisMode(value)
			if mode == "" {
				return "", "", fmt.Errorf("invalid analyze-project mode %q; expected one of %s", value, strings.Join(supportedProjectAnalysisModes, ", "))
			}
		default:
			goalParts = append(goalParts, field)
		}
	}
	goal := strings.TrimSpace(strings.Join(goalParts, " "))
	if goal == "" {
		return "", "", fmt.Errorf(projectAnalysisUsage())
	}
	return mode, goal, nil
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
	rt.autoCP.Arm(configAutoCheckpointEdits(rt.cfg), workspaceSnapshotRoot(rt.workspace))
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
		rt.printWhileThinking(rt.ui.infoLine("Creating automatic checkpoint before edit..."))
	}
	meta, err := rt.autoCP.Prepare(reason)
	if err != nil {
		return err
	}
	if message := formatAutoCheckpointMessage(meta); message != "" {
		rt.printWhileThinking(rt.ui.infoLine(message))
	}
	return nil
}

func workspaceSnapshotRoot(ws Workspace) string {
	if strings.TrimSpace(ws.BaseRoot) != "" {
		return ws.BaseRoot
	}
	return ws.Root
}

func (rt *runtimeState) handleInitCommand(args string) error {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		path := filepath.Join(rt.session.WorkingDir, "KERNFORGE.md")
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists", path)
		}
		projectName := filepath.Base(rt.session.WorkingDir)
		if err := os.WriteFile(path, []byte(InitMemoryTemplate(projectName)), 0o644); err != nil {
			return err
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Created "+path))
		rt.memory, _ = LoadMemory(rt.session.WorkingDir, rt.cfg.MemoryFiles)
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
		if err := os.WriteFile(path, []byte(InitWorkspaceConfigTemplate()), 0o644); err != nil {
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
