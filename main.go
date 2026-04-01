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
	"path/filepath"
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
	cfg           Config
	reader        *bufio.Reader
	writer        io.Writer
	ui            UI
	bannerShown   bool
	prefillInput  string
	inputHistory  []string
	store         *SessionStore
	session       *Session
	agent         *Agent
	perms         *PermissionManager
	memory        MemoryBundle
	longMem       *PersistentMemoryStore
	checkpoints   *CheckpointManager
	autoCP        *AutoCheckpointController
	verifyHistory *VerificationHistoryStore
	skills        SkillCatalog
	skillWarns    []string
	mcp           *MCPManager
	mcpWarns      []string
	ollamaModels  []OllamaModelInfo
	clientErr     error
	workspace     Workspace
	interactive   bool
	thinkingMu    sync.Mutex
	thinkingStop  func()
	requestCancelMu     sync.Mutex
	requestCancelPauses int
	lastAssistantMu      sync.Mutex
	lastAssistantPrinted string
}

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
		cfg:           cfg,
		reader:        bufio.NewReader(os.Stdin),
		writer:        os.Stdout,
		ui:            NewUI(),
		store:         store,
		session:       sess,
		memory:        mem,
		longMem:       NewPersistentMemoryStore(),
		checkpoints:   NewCheckpointManager(),
		autoCP:        &AutoCheckpointController{},
		verifyHistory: NewVerificationHistoryStore(),
		interactive:   promptFlag == "",
	}
	defer rt.closeExtensions()

	if rt.interactive {
		rt.showBanner()
	}

	rt.perms = NewPermissionManager(ParseMode(sess.PermissionMode), rt.confirm)
	rt.workspace = Workspace{
		BaseRoot:    cwd,
		Root:        sess.WorkingDir,
		Shell:       cfg.Shell,
		Perms:       rt.perms,
		PrepareEdit: rt.prepareEdit,
		CurrentSelection: func() *ViewerSelection {
			return rt.session.CurrentSelection()
		},
		PreviewEdit: rt.previewEdit,
		UpdatePlan: func(items []PlanItem) {
			rt.session.Plan = append([]PlanItem(nil), items...)
			_ = rt.store.Save(rt.session)
		},
		GetPlan: func() []PlanItem {
			return append([]PlanItem(nil), rt.session.Plan...)
		},
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
		VerifyHistory: rt.verifyHistory,
		EmitAssistant: func(text string) {
			rt.printAssistantWhileThinking(text)
		},
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
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
	if !openPreview {
		return false, ErrEditCanceled
	}
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
	return rt.confirm("Apply these changes?")
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
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Type /help for commands. End a line with \\ for multiline input."))
}

func (rt *runtimeState) runREPL() error {
	rt.redrawScreen()

	for {
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
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		reply, err := rt.runAgentReply(ctx, input)
		cancel()
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
			fmt.Fprintln(rt.writer, rt.ui.errorLine("assistant error: "+err.Error()))
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

func (rt *runtimeState) runAgentReplyWithImages(ctx context.Context, input string, images []MessageImage) (string, error) {
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	rt.resetAssistantDedup()
	rt.armAutoCheckpoint()
	defer rt.clearAutoCheckpoint()

	stopEscapeWatcher := startEscapeWatcher(cancel, rt.shouldHonorRequestCancel)
	defer stopEscapeWatcher()

	rt.startThinkingIndicator()
	defer rt.stopThinkingIndicator()

	reply, err := rt.agent.ReplyWithImages(requestCtx, input, images)
	if err != nil {
		if requestCtx.Err() == context.Canceled && ctx.Err() == nil {
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
		startedAt := time.Now()
		render := func(frame string) {
			line := rt.ui.thinkingLine(frame, time.Since(startedAt))
			clear := "\r" + strings.Repeat(" ", lastWidth) + "\r"
			fmt.Fprint(rt.writer, clear+line)
			lastWidth = visibleLen(line)
		}

		index := 0
		render(frames[index])
		for {
			select {
			case <-stop:
				clear := "\r" + strings.Repeat(" ", lastWidth) + "\r"
				fmt.Fprint(rt.writer, clear)
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

func (rt *runtimeState) printWhileThinking(lines ...string) {
	rt.stopThinkingIndicator()
	for _, line := range lines {
		fmt.Fprintln(rt.writer, line)
	}
	rt.startThinkingIndicator()
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
	fmt.Fprintln(rt.writer, rt.ui.assistant(text))
}

func (rt *runtimeState) printAssistantWhileThinking(text string) {
	if !rt.shouldPrintAssistant(text) {
		return
	}
	rt.stopThinkingIndicator()
	fmt.Fprintln(rt.writer, rt.ui.assistant(text))
	rt.startThinkingIndicator()
}

func (rt *runtimeState) shouldHonorRequestCancel() bool {
	rt.requestCancelMu.Lock()
	defer rt.requestCancelMu.Unlock()
	return rt.requestCancelPauses == 0
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
	for {
		var historyNav *inputHistoryNavigator
		if len(lines) == 0 {
			historyNav = newInputHistoryNavigator(rt.inputHistoryEntries(), initial)
		}
		line, usedInteractive, err := rt.readInteractiveLine(currentPrompt, initial, historyNav)
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

func (rt *runtimeState) confirm(question string) (bool, error) {
	if !rt.interactive {
		return false, fmt.Errorf("interactive confirmation unavailable")
	}
	resumeThinking := rt.suspendThinkingIndicator()
	defer resumeThinking()
	for {
		label := rt.ui.warnLine(question) + " " + rt.ui.dim("[y/N, Esc=cancel]")
		answer, usedInteractive, err := rt.readInteractiveLine(label+" ", "", nil)
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
		answer = strings.ToLower(strings.TrimSpace(answer))
		switch answer {
		case "y", "yes":
			return true, nil
		case "", "n", "no":
			return false, nil
		}
	}
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
	line, usedInteractive, err := rt.readInteractiveLine(label+" ", "", nil)
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
	base := rt.cfg.BaseURL
	if strings.ToLower(rt.cfg.Provider) == "ollama" || strings.TrimSpace(base) != "" {
		base = normalizeOllamaBaseURL(base)
	}
	if base == "" {
		base = normalizeOllamaBaseURL("")
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Provider"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("provider", valueOrUnset(rt.session.Provider)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("base_url", base))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("model", valueOrUnset(rt.session.Model)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("cached_models", fmt.Sprintf("%d", len(rt.ollamaModels))))
	return nil
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
		fmt.Fprintln(rt.writer, rt.ui.statusKV("version", currentVersion()))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("provider", rt.session.Provider))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("model", rt.session.Model))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("base_url", rt.cfg.BaseURL))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("permission_mode", rt.session.PermissionMode))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("cwd", rt.session.WorkingDir))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("session", rt.session.ID))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("sessions_dir", rt.store.Root()))
		if rt.session.LastSelection != nil && rt.session.LastSelection.HasSelection() {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("selection", rt.session.LastSelection.Summary(rt.workspace.Root)))
		}
		fmt.Fprintln(rt.writer, rt.ui.statusKV("memory_files", fmt.Sprintf("%d", len(rt.memory.Files))))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("persistent_memory", fmt.Sprintf("%d", rt.persistentMemoryCount())))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("auto_checkpoint_edits", fmt.Sprintf("%t", configAutoCheckpointEdits(rt.cfg))))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("auto_locale", fmt.Sprintf("%t", configAutoLocale(rt.cfg))))
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
		for _, warning := range append(append([]string(nil), rt.skillWarns...), rt.mcpWarns...) {
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
	case "clear", "reset", "new":
		rt.session.Messages = nil
		rt.session.Summary = ""
		rt.session.Plan = nil
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
		fmt.Fprintln(rt.writer, rt.ui.successLine("Reloaded config, memory, skills, and MCP extensions"))
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
		for _, item := range rt.session.Plan {
			status := item.Status
			switch item.Status {
			case "completed":
				status = rt.ui.success(item.Status)
			case "in_progress":
				status = rt.ui.accent2(item.Status)
			default:
				status = rt.ui.dim(item.Status)
			}
			fmt.Fprintf(rt.writer, "[%s] %s\n", status, item.Step)
		}
	case "provider":
		return false, rt.handleProviderCommand(cmd.Args)
	case "profile":
		return false, rt.handleProfileCommand()
	case "diff":
		out, err := NewGitDiffTool(rt.workspace).Execute(context.Background(), map[string]any{})
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
		fmt.Fprintln(rt.writer, rt.ui.statusKV("provider", rt.cfg.Provider))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("model", rt.cfg.Model))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("base_url", rt.cfg.BaseURL))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("shell", rt.cfg.Shell))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("permission_mode", string(rt.perms.Mode())))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("session_dir", rt.cfg.SessionDir))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("max_tool_iterations", fmt.Sprintf("%d", configMaxToolIterations(rt.cfg))))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("auto_compact_chars", fmt.Sprintf("%d", rt.cfg.AutoCompactChars)))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("auto_checkpoint_edits", fmt.Sprintf("%t", configAutoCheckpointEdits(rt.cfg))))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("auto_verify_docs_only", fmt.Sprintf("%t", configAutoVerifyDocsOnly(rt.cfg))))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("auto_locale", fmt.Sprintf("%t", configAutoLocale(rt.cfg))))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("skill_paths", fmt.Sprintf("%d", len(rt.cfg.SkillPaths))))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("enabled_skills", strings.Join(rt.cfg.EnabledSkills, ", ")))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("mcp_servers", fmt.Sprintf("%d", len(rt.cfg.MCPServers))))
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
	case "do-plan-review":
		if err := rt.handleDoPlanReviewCommand(cmd.Args); err != nil {
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

	stopEscapeWatcher := startEscapeWatcher(cancel, rt.shouldHonorRequestCancel)
	defer stopEscapeWatcher()

	result, err := RunPlanReview(
		requestCtx,
		rt.agent.Client,
		rt.session.Model,
		reviewerClient,
		rt.cfg.PlanReview.Model,
		args,
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
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Executing plan..."))
	reply, err := rt.runAgentReply(requestCtx, executionPrompt)
	if err != nil {
		return err
	}
	rt.printAssistant(reply)
	return nil
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
	rt.perms.SetMode(ParseMode(rt.session.PermissionMode))
	rt.store = NewSessionStore(rt.cfg.SessionDir)

	rt.memory, err = LoadMemory(rt.session.WorkingDir, rt.cfg.MemoryFiles)
	if err != nil {
		return err
	}
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

func (rt *runtimeState) handlePersistentMemoryRecent(args string) error {
	if rt.longMem == nil {
		return fmt.Errorf("persistent memory is not configured")
	}
	if strings.TrimSpace(args) != "" {
		return rt.handlePersistentMemorySearch(args)
	}
	records, err := rt.longMem.ListRecent(rt.workspace.BaseRoot, 8)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No persistent memory records found for this workspace."))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Persistent Memory"))
	for _, record := range records {
		fmt.Fprintf(rt.writer, "%s  importance=%s  trust=%s  %s\n", rt.ui.dim(record.Citation()), record.ImportanceLabel(), record.TrustLabel(), compactPersistentMemoryText(record.Summary, 220))
	}
	return nil
}

func (rt *runtimeState) handlePersistentMemorySearch(query string) error {
	if rt.longMem == nil {
		return fmt.Errorf("persistent memory is not configured")
	}
	if strings.TrimSpace(query) == "" {
		return fmt.Errorf("usage: /mem-search <query>")
	}
	records, err := rt.longMem.SearchHits(query, rt.workspace.BaseRoot, rt.session.ID, 8)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No persistent memory matched that query."))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Memory Search"))
	for _, hit := range records {
		fmt.Fprintf(rt.writer, "%s  importance=%s  trust=%s  score=%d  %s\n", rt.ui.dim(hit.Citation), hit.Record.ImportanceLabel(), hit.Record.TrustLabel(), hit.Score, compactPersistentMemoryText(hit.Record.Summary, 260))
	}
	return nil
}

func (rt *runtimeState) handlePersistentMemoryShow(id string) error {
	if rt.longMem == nil {
		return fmt.Errorf("persistent memory is not configured")
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("usage: /mem-show <id>")
	}
	record, ok, err := rt.longMem.Get(id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("persistent memory record not found: %s", id)
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Memory Record"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("citation", record.Citation()))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("importance", record.ImportanceLabel()))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("trust", record.TrustLabel()))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("workspace", record.Workspace))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("request", valueOrUnset(record.Request)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("reply", valueOrUnset(record.Reply)))
	if strings.TrimSpace(record.VerificationSummary) != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("verification", record.VerificationSummary))
	}
	if len(record.Files) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("refs", strings.Join(record.Files, ", ")))
	}
	if len(record.ToolNames) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("tools", strings.Join(record.ToolNames, ", ")))
	}
	return nil
}

func (rt *runtimeState) handlePersistentMemoryAdjust(id, action string) error {
	if rt.longMem == nil {
		return fmt.Errorf("persistent memory is not configured")
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("usage: /mem-%s <id>", action)
	}
	var (
		record PersistentMemoryRecord
		ok     bool
		err    error
	)
	switch action {
	case "promote":
		record, ok, err = rt.longMem.Promote(id)
	case "demote":
		record, ok, err = rt.longMem.Demote(id)
	case "confirm":
		record, ok, err = rt.longMem.SetTrust(id, PersistentMemoryConfirmed)
	case "tentative":
		record, ok, err = rt.longMem.SetTrust(id, PersistentMemoryTentative)
	default:
		return fmt.Errorf("unsupported memory action: %s", action)
	}
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("persistent memory record not found: %s", id)
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Updated %s -> importance=%s trust=%s", record.Citation(), record.ImportanceLabel(), record.TrustLabel())))
	return nil
}

func (rt *runtimeState) handlePersistentMemoryDashboard(query string, html bool) error {
	if rt.longMem == nil {
		return fmt.Errorf("persistent memory is not configured")
	}
	summary, err := rt.longMem.Dashboard(workspaceSnapshotRoot(rt.workspace), query, 12)
	if err != nil {
		return err
	}
	if html {
		outputPath, err := createPersistentMemoryDashboardHTML(summary)
		if err != nil {
			return err
		}
		if err := OpenExternalURL(outputPath); err != nil {
			fmt.Fprintln(rt.writer, rt.ui.warnLine("Generated HTML memory dashboard but could not open it automatically: "+err.Error()))
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Generated memory dashboard: "+outputPath))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Memory Dashboard"))
	fmt.Fprintln(rt.writer, renderPersistentMemoryDashboard(summary))
	return nil
}

func (rt *runtimeState) handlePersistentMemoryPrune(args string) error {
	if rt.longMem == nil {
		return fmt.Errorf("persistent memory is not configured")
	}
	all := strings.EqualFold(strings.TrimSpace(args), "all")
	workspace := workspaceSnapshotRoot(rt.workspace)
	policy, err := LoadPersistentMemoryPolicy(workspace)
	if err != nil {
		return err
	}
	result, err := rt.longMem.Prune(workspace, policy, all)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Memory Prune"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("scope", result.Scope))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("before", fmt.Sprintf("%d", result.Before)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("after", fmt.Sprintf("%d", result.After)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("deleted", fmt.Sprintf("%d", result.Deleted)))
	for i := range result.DeletedIDs {
		fmt.Fprintf(rt.writer, "%s  %s\n", rt.ui.dim(result.DeletedIDs[i]), result.DeletedReason[i])
	}
	return nil
}

func (rt *runtimeState) requireSelection() (ViewerSelection, error) {
	selection := rt.session.CurrentSelection()
	if selection == nil || !selection.HasSelection() {
		return ViewerSelection{}, fmt.Errorf("no current selection. Use /open and select a range first")
	}
	return *selection, nil
}

func (rt *runtimeState) handleSelectionCommand() error {
	selection, err := rt.requireSelection()
	if err != nil {
		return err
	}
	preview, err := loadSelectionPreview(rt.workspace.Root, selection)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Selection"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("range", selection.Summary(rt.workspace.Root)))
	fmt.Fprintln(rt.writer, preview)
	return nil
}

func (rt *runtimeState) handleSelectionsCommand() error {
	rt.session.normalizeSelectionState()
	if len(rt.session.Selections) == 0 {
		return fmt.Errorf("no saved selections. Use /open and select a range first")
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Selections"))
	for i, selection := range rt.session.Selections {
		suffix := ""
		if i == rt.session.ActiveSelection {
			suffix = " [active]"
		}
		fmt.Fprintf(rt.writer, "%d. %s%s\n", i+1, selection.Summary(rt.workspace.Root), suffix)
	}
	return nil
}

func (rt *runtimeState) handleSelectionNoteCommand(note string) error {
	selection := rt.session.CurrentSelection()
	if selection == nil || !selection.HasSelection() {
		return fmt.Errorf("no current selection. Use /open and select a range first")
	}
	if strings.TrimSpace(note) == "" {
		return fmt.Errorf("usage: /note-selection <text>")
	}
	rt.session.Selections[rt.session.ActiveSelection].Note = strings.TrimSpace(note)
	active := rt.session.Selections[rt.session.ActiveSelection]
	rt.session.LastSelection = &active
	_ = rt.store.Save(rt.session)
	_ = SyncWorkspaceSelections(rt.workspace.Root, rt.session.Selections)
	fmt.Fprintln(rt.writer, rt.ui.successLine("Updated note on active selection and synced to workspace"))
	return nil
}

func (rt *runtimeState) handleSelectionTagCommand(tags string) error {
	selection := rt.session.CurrentSelection()
	if selection == nil || !selection.HasSelection() {
		return fmt.Errorf("no current selection. Use /open and select a range first")
	}
	if strings.TrimSpace(tags) == "" {
		return fmt.Errorf("usage: /tag-selection <tag[,tag2,...]>")
	}
	rt.session.Selections[rt.session.ActiveSelection].SetTags(tags)
	active := rt.session.Selections[rt.session.ActiveSelection]
	rt.session.LastSelection = &active
	_ = rt.store.Save(rt.session)
	_ = SyncWorkspaceSelections(rt.workspace.Root, rt.session.Selections)
	fmt.Fprintln(rt.writer, rt.ui.successLine("Updated tags on active selection and synced to workspace"))
	return nil
}

func (rt *runtimeState) handleUseSelectionCommand(arg string) error {
	index, err := parsePositiveInt(strings.TrimSpace(arg))
	if err != nil || index < 1 {
		return fmt.Errorf("usage: /use-selection <n>")
	}
	if !rt.session.SetActiveSelection(index - 1) {
		return fmt.Errorf("selection index out of range: %d", index)
	}
	_ = rt.store.Save(rt.session)
	fmt.Fprintln(rt.writer, rt.ui.successLine("Active selection set to "+rt.session.CurrentSelection().Summary(rt.workspace.Root)))
	return nil
}

func (rt *runtimeState) handleDropSelectionCommand(arg string) error {
	index, err := parsePositiveInt(strings.TrimSpace(arg))
	if err != nil || index < 1 {
		return fmt.Errorf("usage: /drop-selection <n>")
	}
	if !rt.session.RemoveSelection(index - 1) {
		return fmt.Errorf("selection index out of range: %d", index)
	}
	_ = rt.store.Save(rt.session)
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Dropped selection %d", index)))
	return nil
}

func (rt *runtimeState) handleSelectionDiffCommand() error {
	selection, err := rt.requireSelection()
	if err != nil {
		return err
	}
	diff, err := renderSelectionGitDiff(rt.workspace.Root, selection)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Selection Diff"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("range", selection.Summary(rt.workspace.Root)))
	fmt.Fprintln(rt.writer, diff)
	return nil
}

func (rt *runtimeState) handleSelectionReviewCommand(extra string) error {
	selection, err := rt.requireSelection()
	if err != nil {
		return err
	}
	prompt := selection.RelativePrompt(rt.workspace.Root) + " review only this selected code. Focus on bugs, risks, regressions, and missing tests."
	if strings.TrimSpace(extra) != "" {
		prompt += " " + strings.TrimSpace(extra)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	reply, err := rt.runAgentReply(ctx, prompt)
	if err != nil {
		return err
	}
	if strings.TrimSpace(reply) != "" {
		rt.printAssistant(reply)
	}
	return nil
}

func (rt *runtimeState) handleSelectionsReviewCommand(args string) error {
	rt.session.normalizeSelectionState()
	if len(rt.session.Selections) == 0 {
		return fmt.Errorf("no saved selections. Use /open and select a range first")
	}
	selected, extra, err := parseSelectionReviewArgs(rt.session, args)
	if err != nil {
		return err
	}
	prompt := buildSelectionContextPrompt(rt.workspace.Root, selected) + " review only these selected code regions together. Focus on bugs, risks, regressions, duplication, and missing tests across the selected regions."
	if strings.TrimSpace(extra) != "" {
		prompt += " " + strings.TrimSpace(extra)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	reply, err := rt.runAgentReply(ctx, prompt)
	if err != nil {
		return err
	}
	if strings.TrimSpace(reply) != "" {
		rt.printAssistant(reply)
	}
	return nil
}

func (rt *runtimeState) handleSelectionEditCommand(task string) error {
	selection, err := rt.requireSelection()
	if err != nil {
		return err
	}
	if strings.TrimSpace(task) == "" {
		return fmt.Errorf("usage: /edit-selection <task>")
	}
	prompt := selection.RelativePrompt(rt.workspace.Root) + " edit this selected code. Keep the change strictly focused on the selected range unless adjacent lines must change for correctness. Avoid unrelated edits outside the selection. If you must touch code outside the selection, keep it minimal and explain why in the final answer. Task: " + strings.TrimSpace(task)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	reply, err := rt.runAgentReply(ctx, prompt)
	if err != nil {
		return err
	}
	if strings.TrimSpace(reply) != "" {
		rt.printAssistant(reply)
	}
	return nil
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

func (rt *runtimeState) handlePersistentMemoryStats() error {
	if rt.longMem == nil {
		return fmt.Errorf("persistent memory is not configured")
	}
	stats, err := rt.longMem.Stats()
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Memory Stats"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("path", stats.Path))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("records", fmt.Sprintf("%d", stats.Count)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("workspaces", fmt.Sprintf("%d", stats.WorkspaceSet)))
	if !stats.LastUpdated.IsZero() {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("last_updated", stats.LastUpdated.Format(time.RFC3339)))
	}
	return nil
}

func (rt *runtimeState) handleVerifyCommand(args string) error {
	changed := collectVerificationChangedPaths(rt.workspace.Root, rt.session)
	mode := VerificationAdaptive
	if strings.TrimSpace(args) != "" {
		override := []string{}
		for _, item := range strings.Fields(args) {
			if strings.EqualFold(strings.TrimSpace(item), "--full") {
				mode = VerificationFull
				continue
			}
			for _, path := range strings.Split(item, ",") {
				if value := normalizeVerificationOverridePath(path); value != "" {
					override = append(override, value)
				}
			}
		}
		if len(override) > 0 {
			changed = override
		}
	}
	tuning := VerificationTuning{}
	if rt.verifyHistory != nil {
		if loaded, err := rt.verifyHistory.PlannerTuning(rt.workspace.Root); err == nil {
			tuning = loaded
		}
	}
	plan := buildVerificationPlanWithTuning(rt.workspace.Root, changed, mode, tuning)
	if len(plan.Steps) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No recommended verification steps were found for this workspace."))
		return nil
	}
	report := executeVerificationSteps(context.Background(), rt.workspace, "manual", plan)
	rt.session.LastVerification = &report
	_ = rt.store.Save(rt.session)
	if rt.verifyHistory != nil {
		_ = rt.verifyHistory.Append(rt.session.ID, workspaceSnapshotRoot(rt.workspace), report)
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Verification"))
	fmt.Fprintln(rt.writer, report.RenderDetailed())
	return nil
}

func (rt *runtimeState) handleVerifyDashboardCommand(args string) error {
	if rt.verifyHistory == nil {
		return fmt.Errorf("verification history is not configured")
	}
	all, tags := parseVerificationDashboardArgs(args)
	summary, err := rt.verifyHistory.Dashboard(workspaceSnapshotRoot(rt.workspace), all, tags, 10)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Verification Dashboard"))
	fmt.Fprintln(rt.writer, renderVerificationDashboard(summary))
	return nil
}

func (rt *runtimeState) handleVerifyDashboardHTMLCommand(args string) error {
	if rt.verifyHistory == nil {
		return fmt.Errorf("verification history is not configured")
	}
	all, tags := parseVerificationDashboardArgs(args)
	summary, err := rt.verifyHistory.Dashboard(workspaceSnapshotRoot(rt.workspace), all, tags, 20)
	if err != nil {
		return err
	}
	outputPath, err := createVerificationDashboardHTML(summary, all)
	if err != nil {
		return err
	}
	if err := OpenExternalURL(outputPath); err != nil {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("Generated HTML dashboard but could not open it automatically: "+err.Error()))
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("Generated verification dashboard: "+outputPath))
	return nil
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

func parseCheckpointTargetAndPaths(raw string) (string, []string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "latest", nil, nil
	}
	target := trimmed
	var paths []string
	if idx := strings.Index(trimmed, " -- "); idx >= 0 {
		target = strings.TrimSpace(trimmed[:idx])
		pathPart := strings.TrimSpace(trimmed[idx+4:])
		for _, item := range strings.Split(pathPart, ",") {
			if value := strings.TrimSpace(item); value != "" {
				paths = append(paths, value)
			}
		}
	}
	if target == "" {
		target = "latest"
	}
	return target, paths, nil
}

func (rt *runtimeState) handleCheckpointCommand(args string) error {
	if rt.checkpoints == nil {
		return fmt.Errorf("checkpoint manager is not configured")
	}
	name := strings.TrimSpace(args)
	if name == "" && rt.interactive {
		value, err := rt.promptValueAllowEmpty("Checkpoint note (optional)", "")
		if err != nil {
			if errors.Is(err, ErrPromptCanceled) {
				return fmt.Errorf("checkpoint creation canceled")
			}
			return err
		}
		name = strings.TrimSpace(value)
	}
	ok, err := rt.perms.Allow(ActionWrite, "create checkpoint for "+workspaceSnapshotRoot(rt.workspace))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("checkpoint creation canceled")
	}
	meta, err := rt.checkpoints.Create(workspaceSnapshotRoot(rt.workspace), name)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Created checkpoint %s (%s)", meta.ID, meta.Name)))
	return nil
}

func (rt *runtimeState) handleCheckpointAutoCommand(args string) error {
	if strings.TrimSpace(args) == "" {
		fmt.Fprintln(rt.writer, rt.ui.infoLine(fmt.Sprintf("Automatic checkpoint before edits: %t", configAutoCheckpointEdits(rt.cfg))))
		return nil
	}
	value, ok := parseBoolString(args)
	if !ok {
		return fmt.Errorf("usage: /checkpoint-auto [on|off]")
	}
	rt.cfg.AutoCheckpointEdits = boolPtr(value)
	if err := rt.saveUserConfig(); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Automatic checkpoint before edits set to %t", value)))
	return nil
}

func (rt *runtimeState) handleCheckpointsCommand() error {
	if rt.checkpoints == nil {
		return fmt.Errorf("checkpoint manager is not configured")
	}
	items, err := rt.checkpoints.List(workspaceSnapshotRoot(rt.workspace))
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No checkpoints found for this workspace."))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Checkpoints"))
	for _, item := range items {
		fmt.Fprintf(rt.writer, "%s  %s  files=%d  size=%dB\n", rt.ui.dim(item.ID), item.Name, item.FileCount, item.TotalBytes)
	}
	return nil
}

func (rt *runtimeState) handleCheckpointDiffCommand(args string) error {
	if rt.checkpoints == nil {
		return fmt.Errorf("checkpoint manager is not configured")
	}
	target, paths, err := parseCheckpointTargetAndPaths(args)
	if err != nil {
		return err
	}
	meta, diffs, err := rt.checkpoints.Diff(workspaceSnapshotRoot(rt.workspace), target, paths)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Checkpoint Diff"))
	fmt.Fprintln(rt.writer, rt.ui.dim(fmt.Sprintf("%s  %s", meta.ID, meta.Name)))
	fmt.Fprintln(rt.writer, renderCheckpointDiff(diffs))
	return nil
}

func (rt *runtimeState) handleRollbackCommand(args string) error {
	if rt.checkpoints == nil {
		return fmt.Errorf("checkpoint manager is not configured")
	}
	trimmedArgs := strings.TrimSpace(args)
	if trimmedArgs == "" || strings.EqualFold(trimmedArgs, "pick") || strings.EqualFold(trimmedArgs, "choose") {
		selected, err := rt.pickRollbackCheckpoint()
		if err != nil {
			return err
		}
		if strings.TrimSpace(selected) == "" {
			return fmt.Errorf("rollback canceled")
		}
		args = selected
	}
	target, paths, err := parseCheckpointTargetAndPaths(args)
	if err != nil {
		return err
	}
	meta, _, err := rt.checkpoints.Resolve(workspaceSnapshotRoot(rt.workspace), target)
	if err != nil {
		return err
	}
	ok, err := rt.perms.Allow(ActionWrite, "rollback workspace to checkpoint "+meta.ID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("rollback canceled")
	}
	if rt.interactive {
		scope := "workspace"
		if len(paths) > 0 {
			scope = strings.Join(paths, ", ")
		}
		confirm, err := rt.confirm(fmt.Sprintf("Rollback %s to checkpoint %s (%s)? This will overwrite current files.", scope, meta.ID, meta.Name))
		if err != nil {
			return err
		}
		if !confirm {
			return fmt.Errorf("rollback canceled")
		}
	}
	safetyName := "pre-rollback-" + time.Now().Format("20060102-150405")
	if safety, err := rt.checkpoints.Create(workspaceSnapshotRoot(rt.workspace), safetyName); err == nil {
		fmt.Fprintln(rt.writer, rt.ui.infoLine("Created safety checkpoint "+safety.ID))
	}
	var restored CheckpointMetadata
	if len(paths) > 0 {
		restored, err = rt.checkpoints.RollbackPaths(workspaceSnapshotRoot(rt.workspace), meta.ID, paths)
	} else {
		restored, err = rt.checkpoints.Rollback(workspaceSnapshotRoot(rt.workspace), meta.ID)
	}
	if err != nil {
		return err
	}
	if err := rt.reloadRuntimeConfig(); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Rolled back to checkpoint %s (%s)", restored.ID, restored.Name)))
	return nil
}

func (rt *runtimeState) pickRollbackCheckpoint() (string, error) {
	items, err := rt.checkpoints.List(workspaceSnapshotRoot(rt.workspace))
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No checkpoints found for this workspace."))
		return "", nil
	}

	fmt.Fprintln(rt.writer, rt.ui.section("Choose Checkpoint"))
	for i, item := range items {
		fmt.Fprintf(rt.writer, "%s  %s  %s  files=%d  size=%dB\n",
			rt.ui.accent(fmt.Sprintf("%2d.", i+1)),
			rt.ui.dim(item.ID),
			item.Name,
			item.FileCount,
			item.TotalBytes,
		)
	}
	fmt.Fprintln(rt.writer, rt.ui.hintLine("Enter a checkpoint number, or press Enter to cancel."))

	for {
		answer, err := rt.readInput(rt.ui.accent("rollback pick") + rt.ui.dim(" > "))
		if err != nil {
			if errors.Is(err, ErrPromptCanceled) {
				return "", nil
			}
			return "", err
		}
		answer = strings.TrimSpace(answer)
		if answer == "" {
			return "", nil
		}
		index, convErr := strconv.Atoi(answer)
		if convErr != nil || index < 1 || index > len(items) {
			fmt.Fprintln(rt.writer, rt.ui.warnLine(fmt.Sprintf("Choose a number between 1 and %d.", len(items))))
			continue
		}
		return items[index-1].ID, nil
	}
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
		return fmt.Errorf("usage: /init [memory-policy|skill <name>|config|verify]")
	}
}
