package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type blockingLineInput struct {
	started   chan struct{}
	release   chan struct{}
	remaining string
	once      sync.Once
}

func (b *blockingLineInput) Read(p []byte) (int, error) {
	b.once.Do(func() {
		close(b.started)
	})
	<-b.release
	if b.remaining == "" {
		return 0, io.EOF
	}
	n := copy(p, b.remaining)
	b.remaining = b.remaining[n:]
	return n, nil
}

func TestRuntimeStateWithRequestCancelSuspendedDisablesAndRestores(t *testing.T) {
	rt := &runtimeState{}

	if !rt.shouldHonorRequestCancel() {
		t.Fatalf("expected request cancel to be enabled by default")
	}

	called := false
	rt.withRequestCancelSuspended(func() {
		called = true
		if rt.shouldHonorRequestCancel() {
			t.Fatalf("expected request cancel to be disabled inside suspension scope")
		}
	})

	if !called {
		t.Fatalf("expected suspension callback to run")
	}
	if !rt.shouldHonorRequestCancel() {
		t.Fatalf("expected request cancel to be restored after suspension scope")
	}
}

func TestRuntimeStateWithRequestCancelSuspendedSupportsNesting(t *testing.T) {
	rt := &runtimeState{}

	rt.withRequestCancelSuspended(func() {
		if rt.shouldHonorRequestCancel() {
			t.Fatalf("expected outer suspension to disable request cancel")
		}

		rt.withRequestCancelSuspended(func() {
			if rt.shouldHonorRequestCancel() {
				t.Fatalf("expected nested suspension to keep request cancel disabled")
			}
		})

		if rt.shouldHonorRequestCancel() {
			t.Fatalf("expected outer suspension to remain active after nested scope")
		}
	})

	if !rt.shouldHonorRequestCancel() {
		t.Fatalf("expected request cancel to be restored after nested suspensions")
	}
}

func TestRuntimeStateRequestCancelWatcherIsInteractiveOnly(t *testing.T) {
	rt := &runtimeState{interactive: false}
	if rt.shouldInstallRequestCancelWatcher() {
		t.Fatalf("non-interactive single-shot runs must not install the ambient request-cancel watcher")
	}
	if rt.confirmRequestCancel() {
		t.Fatalf("non-interactive request cancel must not be implicitly confirmed by ambient input")
	}

	rt.interactive = true
	if !rt.shouldInstallRequestCancelWatcher() {
		t.Fatalf("interactive runs should keep the request-cancel watcher available")
	}
}

func TestRuntimeShouldUseInteractiveLoopOnlyForSessionMode(t *testing.T) {
	tests := []struct {
		name         string
		promptFlag   string
		commandFlag  string
		goalFlag     string
		goalFileFlag string
		want         bool
	}{
		{name: "plain session", want: true},
		{name: "prompt", promptFlag: "fix this", want: false},
		{name: "command", commandFlag: "/status", want: false},
		{name: "goal", goalFlag: "finish this", want: false},
		{name: "goal file", goalFileFlag: "goal.md", want: false},
		{name: "trimmed goal file", goalFileFlag: "  goal.md  ", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runtimeShouldUseInteractiveLoop(tt.promptFlag, tt.commandFlag, tt.goalFlag, tt.goalFileFlag)
			if got != tt.want {
				t.Fatalf("runtimeShouldUseInteractiveLoop()=%v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunSinglePromptTreatsUnavailableWriteApprovalAsCanceled(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	original := "package main\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	session := NewSession(root, "scripted", "model", "", "default")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	var out bytes.Buffer
	rt := &runtimeState{
		cfg:         Config{},
		writer:      &out,
		ui:          NewUI(),
		store:       store,
		session:     session,
		interactive: false,
	}
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
		Perms:    NewPermissionManager(ModeDefault, rt.confirm),
	}
	provider := &scriptedProviderClient{
		replies: []ChatResponse{
			toolCallResponse("apply_patch", map[string]any{
				"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n package main\n+func main() {}\n*** End Patch\n",
			}),
			{Message: Message{Role: "assistant", Text: "this response must not be requested"}},
		},
	}
	rt.agent = &Agent{
		Config:    Config{},
		Client:    provider,
		Tools:     NewToolRegistry(NewApplyPatchTool(ws)),
		Workspace: ws,
		Session:   session,
		Store:     store,
	}

	if err := rt.runSinglePrompt("update main.go", nil); err != nil {
		t.Fatalf("runSinglePrompt should treat unavailable write approval as canceled, got %v", err)
	}
	if !strings.Contains(out.String(), "Write canceled.") {
		t.Fatalf("expected user-facing write canceled output, got %q", out.String())
	}
	if len(provider.requests) != 1 {
		t.Fatalf("write approval failure must not request another model turn, got %d", len(provider.requests))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != original {
		t.Fatalf("write approval failure must not change files, got %q", string(data))
	}
}

func TestShouldCancelOnEscapeRejectsCancelWithoutForegroundTarget(t *testing.T) {
	called := false
	allow := func() bool {
		called = true
		return true
	}

	if shouldCancelOnEscape(false, allow) {
		t.Fatalf("expected cancel to be blocked without foreground target")
	}
	if called {
		t.Fatalf("expected shouldCancel callback to be skipped when foreground target is missing")
	}
}

func TestShouldCancelOnEscapeHonorsRuntimeGate(t *testing.T) {
	called := false
	allow := func() bool {
		called = true
		return false
	}

	if shouldCancelOnEscape(true, allow) {
		t.Fatalf("expected cancel to be blocked when runtime gate rejects it")
	}
	if !called {
		t.Fatalf("expected shouldCancel callback to be consulted")
	}
}

func TestShouldCancelOnRepeatedEscapeRejectsWithoutForegroundTarget(t *testing.T) {
	called := false
	allow := func() bool {
		called = true
		return true
	}

	if shouldCancelOnRepeatedEscape(false, true, allow) {
		t.Fatalf("expected repeated escape fallback to be blocked without foreground target")
	}
	if called {
		t.Fatalf("expected cancel gate to be skipped when foreground target is missing")
	}
}

func TestShouldCancelOnRepeatedEscapeStillHonorsRuntimeGate(t *testing.T) {
	called := false
	allow := func() bool {
		called = true
		return false
	}

	if shouldCancelOnRepeatedEscape(true, true, allow) {
		t.Fatalf("expected repeated escape fallback to honor runtime gate")
	}
	if !called {
		t.Fatalf("expected cancel gate to be consulted")
	}
}

func TestConfirmAndCancelCancelsWhenApproved(t *testing.T) {
	canceled := false

	ok := confirmAndCancel(func() bool { return true }, func() {
		canceled = true
	})

	if !ok {
		t.Fatalf("expected confirmAndCancel to return true when approved")
	}
	if !canceled {
		t.Fatalf("expected cancel callback to run when approved")
	}
}

func TestConfirmAndCancelSkipsCancelWhenRejected(t *testing.T) {
	canceled := false

	ok := confirmAndCancel(func() bool { return false }, func() {
		canceled = true
	})

	if ok {
		t.Fatalf("expected confirmAndCancel to return false when rejected")
	}
	if canceled {
		t.Fatalf("expected cancel callback to be skipped when rejected")
	}
}

func TestIsAsyncKeyPressedDetectsDownBit(t *testing.T) {
	if !isAsyncKeyPressed(0x8000) {
		t.Fatalf("expected high-order down bit to be treated as pressed")
	}
}

func TestIsAsyncKeyPressedDetectsTransitionBit(t *testing.T) {
	if !isAsyncKeyPressed(0x0001) {
		t.Fatalf("expected low-order transition bit to be treated as pressed")
	}
}

func TestIsAsyncKeyPressedRejectsClearState(t *testing.T) {
	if isAsyncKeyPressed(0x0000) {
		t.Fatalf("expected clear state to be treated as not pressed")
	}
}

func TestIsEscapePhysicallyPressedDefaultNonWindowsHelperShape(t *testing.T) {
	_ = isEscapePhysicallyPressed()
}

func TestRuntimeStateShouldIgnorePromptEscapeAfterRecentCancel(t *testing.T) {
	rt := &runtimeState{}

	rt.noteRecentRequestCancel()

	if !rt.shouldIgnorePromptEscape() {
		t.Fatalf("expected prompt escape to be ignored immediately after request cancel")
	}
}

func TestRuntimeStateShouldIgnorePromptEscapeExpires(t *testing.T) {
	rt := &runtimeState{}

	rt.requestCancelIgnoreUntil = time.Now().Add(-10 * time.Millisecond)

	if rt.shouldIgnorePromptEscape() {
		t.Fatalf("expected expired cancel grace window to stop ignoring prompt escape")
	}
	if !rt.requestCancelIgnoreUntil.IsZero() {
		t.Fatalf("expected expired cancel grace window to be cleared")
	}
}

func TestRuntimeStateConsumeRecentRequestCancelClearsActiveWindow(t *testing.T) {
	rt := &runtimeState{}

	rt.noteRecentRequestCancel()

	if !rt.consumeRecentRequestCancel() {
		t.Fatalf("expected active recent cancel to be consumed")
	}
	if !rt.requestCancelIgnoreUntil.IsZero() {
		t.Fatalf("expected consumeRecentRequestCancel to clear the grace window")
	}
}

func TestRuntimeStateConsumeRecentRequestCancelRejectsExpiredWindow(t *testing.T) {
	rt := &runtimeState{}

	rt.requestCancelIgnoreUntil = time.Now().Add(-10 * time.Millisecond)

	if rt.consumeRecentRequestCancel() {
		t.Fatalf("expected expired recent cancel to be rejected")
	}
	if !rt.requestCancelIgnoreUntil.IsZero() {
		t.Fatalf("expected expired grace window to be cleared")
	}
}

func TestRuntimeStatePrintTurnSeparatorAddsBreathingRoom(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{color: false},
		session: &Session{
			Provider: "openai",
			Model:    "gpt-5.4",
		},
	}

	rt.printTurnSeparator(3)

	rendered := out.String()
	if !strings.HasPrefix(rendered, "\n----------------") {
		t.Fatalf("expected turn separator to start after a blank line, got %q", rendered)
	}
	if strings.Contains(strings.ToLower(rendered), "turn") {
		t.Fatalf("expected rendered separator to omit explicit turn labels, got %q", rendered)
	}
}

func TestShouldIgnoreEmptyPromptSubmitOnlyForPrimaryBlankSubmit(t *testing.T) {
	if !shouldIgnoreEmptyPromptSubmit(nil, "", "") {
		t.Fatalf("expected top-level empty submit to be ignored")
	}
	if shouldIgnoreEmptyPromptSubmit(nil, "prefill", "") {
		t.Fatalf("expected prefilled prompt to remain submit-capable")
	}
	if shouldIgnoreEmptyPromptSubmit([]string{"partial"}, "", "") {
		t.Fatalf("expected multiline continuation blank submit to remain meaningful")
	}
	if shouldIgnoreEmptyPromptSubmit(nil, "", "   ") {
		t.Fatalf("expected whitespace-only typed content to remain visible to caller")
	}
}

func TestRuntimeStateCurrentThinkingStatusUsesProgressOverride(t *testing.T) {
	rt := &runtimeState{}

	rt.setThinkingStatus("Edit applied. Waiting for the model to summarize the change...")

	status := rt.currentThinkingStatus(3 * time.Second)
	if status != "Edit applied. Waiting for the model to summarize the change..." {
		t.Fatalf("unexpected thinking status: %q", status)
	}
}

func TestRuntimeStateCurrentThinkingStatusPrefersCancelPending(t *testing.T) {
	rt := &runtimeState{}

	rt.setThinkingStatus("Edit applied. Waiting for the model to summarize the change...")
	rt.beginRequestCancel()

	status := rt.currentThinkingStatus(30 * time.Second)
	if status != "Canceling current request..." && status != "취소하는 중 ..." {
		t.Fatalf("expected canceling status, got %q", status)
	}
}

func TestUIThinkingLineSuppressesRedundantGenericThinkingStatus(t *testing.T) {
	ui := UI{}
	line := ansiPattern.ReplaceAllString(ui.thinkingLine("\\", 30*time.Second, "생각 중 ..."), "")
	if strings.Contains(line, "생각 중 ...") {
		t.Fatalf("expected redundant generic thinking status to be omitted, got %q", line)
	}
	if !strings.Contains(line, "[thinking] [\\]") {
		t.Fatalf("expected thinking prefix to remain, got %q", line)
	}
}

func TestUIThinkingLineKeepsSpecificProgressStatus(t *testing.T) {
	ui := UI{}
	line := ansiPattern.ReplaceAllString(ui.thinkingLine("\\", 30*time.Second, "read_file 확인 중 ... VAllocAnalyzer.cpp"), "")
	if !strings.Contains(line, "read_file 확인 중 ... VAllocAnalyzer.cpp") {
		t.Fatalf("expected specific progress status to remain, got %q", line)
	}
}

func TestShouldAnimateThinkingStatusKeepsGenericThinkingAnimated(t *testing.T) {
	if !shouldAnimateThinkingStatus("생각 중 ...") {
		t.Fatalf("expected generic thinking status to remain animated")
	}
	if !shouldAnimateThinkingStatus("") {
		t.Fatalf("expected empty status to remain animated")
	}
}

func TestShouldAnimateThinkingStatusFreezesSpecificToolProgress(t *testing.T) {
	if shouldAnimateThinkingStatus("Using read_file on SampleWorkerCore.cpp:1-200...") {
		t.Fatalf("expected specific tool progress to stop spinner animation")
	}
	if shouldAnimateThinkingStatus("read_file 확인 중 ... SampleWorkerCore.cpp") {
		t.Fatalf("expected localized tool progress to stop spinner animation")
	}
}

func TestDetectLocalPerformanceSignalsFindsCommonPatterns(t *testing.T) {
	content := `
for (;;)
{
    Sleep(10);
    EnterCriticalSection(&g_lock);
    ReadFile(h, buf, size, &read, NULL);
    memcpy(dst, src, size);
    CreateThread(NULL, 0, Worker, NULL, 0, NULL);
}
`
	signals := detectLocalPerformanceSignals(content)
	expected := []string{
		"tight loops",
		"sleep or polling",
		"locking or contention",
		"file io",
		"memory copy or buffer churn",
		"thread fan-out",
	}
	for _, item := range expected {
		if !containsString(signals, item) {
			t.Fatalf("expected signal %q in %#v", item, signals)
		}
	}
	if scoreLocalPerformanceSignals(content) <= 0 {
		t.Fatalf("expected positive local performance score")
	}
}

func TestCollectPerformanceHotspotContextIncludesSignals(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Common"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	code := "for (;;) {\n Sleep(5);\n ReadFile(h, buf, 1, &read, NULL);\n}\n"
	if err := os.WriteFile(filepath.Join(root, "Common", "Hot.cpp"), []byte(code), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	rt := &runtimeState{
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}
	pack := KnowledgePack{}
	lens := PerformanceLens{
		Hotspots: []PerformanceHotspot{
			{Title: "Hot", Files: []string{"Common/Hot.cpp"}},
		},
	}
	context := rt.collectPerformanceHotspotContext(pack, lens)
	if !strings.Contains(context, "FILE Common/Hot.cpp") {
		t.Fatalf("expected file block in hotspot context\n%s", context)
	}
	if !strings.Contains(context, "Signals:") {
		t.Fatalf("expected signals line in hotspot context\n%s", context)
	}
	if !strings.Contains(context, "Local Score:") {
		t.Fatalf("expected local score line in hotspot context\n%s", context)
	}
	if !strings.Contains(strings.ToLower(context), "file io") {
		t.Fatalf("expected file io signal in hotspot context\n%s", context)
	}
}

func TestDetectHotFunctionsFindsFunctionLevelSignals(t *testing.T) {
	content := `
void MonitorLoop()
{
    for (;;)
    {
        Sleep(5);
        ReadFile(h, buf, 1, &read, NULL);
    }
}

void Helper()
{
}
`
	functions := detectHotFunctions(content)
	if len(functions) == 0 {
		t.Fatalf("expected hot function detection")
	}
	if !strings.Contains(functions[0], "MonitorLoop") {
		t.Fatalf("expected MonitorLoop in %#v", functions)
	}
	if !strings.Contains(functions[0], "score=") {
		t.Fatalf("expected function score in %#v", functions)
	}
}

func TestCollectPerformanceHotspotContextIncludesHotFunctions(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Common"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	code := "void MonitorLoop()\n{\n for (;;) {\n  Sleep(5);\n  ReadFile(h, buf, 1, &read, NULL);\n }\n}\n"
	if err := os.WriteFile(filepath.Join(root, "Common", "Hot.cpp"), []byte(code), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	rt := &runtimeState{
		workspace: Workspace{
			BaseRoot: root,
			Root:     root,
		},
	}
	lens := PerformanceLens{
		Hotspots: []PerformanceHotspot{
			{Title: "Hot", Files: []string{"Common/Hot.cpp"}},
		},
	}
	context := rt.collectPerformanceHotspotContext(KnowledgePack{}, lens)
	if !strings.Contains(context, "Hot Functions:") {
		t.Fatalf("expected hot functions line in context\n%s", context)
	}
	if !strings.Contains(context, "score=") {
		t.Fatalf("expected function score in context\n%s", context)
	}
	if !strings.Contains(context, "MonitorLoop") {
		t.Fatalf("expected MonitorLoop in context\n%s", context)
	}
}

func TestPersistPerformanceReportWritesMarkdownAndJSON(t *testing.T) {
	root := t.TempDir()
	rt := &runtimeState{}
	result := PerformanceAnalysisResult{
		Focus:          "startup",
		PrimaryStartup: "SampleApp",
		TopHotspots: []PerformanceHotspot{
			{Title: "Core Runtime", Score: 12},
		},
		Report: "# Performance\n",
	}
	mdPath, jsonPath, err := rt.persistPerformanceReport("# Performance\n", result, "startup", root)
	if err != nil {
		t.Fatalf("persistPerformanceReport returned error: %v", err)
	}
	if _, err := os.Stat(mdPath); err != nil {
		t.Fatalf("expected markdown artifact: %v", err)
	}
	if _, err := os.Stat(jsonPath); err != nil {
		t.Fatalf("expected json artifact: %v", err)
	}
}

func TestShouldCancelOnEscapeAllowsForegroundCancel(t *testing.T) {
	if !shouldCancelOnEscape(true, func() bool { return true }) {
		t.Fatalf("expected cancel to be allowed with foreground target")
	}
}

func TestIsPIDInParentChainMatchesDirectParent(t *testing.T) {
	parents := map[uint32]uint32{
		300: 200,
		200: 100,
	}

	if !isPIDInParentChain(200, 300, mapParentLookup(parents)) {
		t.Fatalf("expected direct parent to match")
	}
}

func TestIsPIDInParentChainMatchesAncestor(t *testing.T) {
	parents := map[uint32]uint32{
		400: 300,
		300: 200,
		200: 100,
	}

	if !isPIDInParentChain(100, 400, mapParentLookup(parents)) {
		t.Fatalf("expected ancestor to match")
	}
}

func TestIsPIDInParentChainRejectsUnrelatedPID(t *testing.T) {
	parents := map[uint32]uint32{
		400: 300,
		300: 200,
		200: 100,
	}

	if isPIDInParentChain(999, 400, mapParentLookup(parents)) {
		t.Fatalf("expected unrelated pid to be rejected")
	}
}

func TestIsPIDInParentChainRejectsCycles(t *testing.T) {
	parents := map[uint32]uint32{
		30: 20,
		20: 10,
		10: 30,
	}

	if isPIDInParentChain(99, 30, mapParentLookup(parents)) {
		t.Fatalf("expected cycle to terminate without match")
	}
}

func mapParentLookup(parents map[uint32]uint32) func(uint32) (uint32, bool) {
	return func(pid uint32) (uint32, bool) {
		parent, ok := parents[pid]
		return parent, ok
	}
}

func TestRuntimeStatePrintAssistantSuppressesEquivalentDuplicateOutput(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
	}

	rt.resetAssistantDedup()
	rt.printAssistant("I prepared a patch. I will show the diff before applying it.")
	rt.printAssistant("  I prepared a patch.\nI will show the diff before applying it.  ")

	rendered := strings.TrimSpace(out.String())
	if strings.Count(rendered, ">> assistant ") != 1 {
		t.Fatalf("expected only one assistant line to be printed, got %q", rendered)
	}
}

func TestRuntimeStatePrintAssistantSuppressesEquivalentDuplicateOutputWithPunctuationChanges(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
	}

	rt.resetAssistantDedup()
	rt.printAssistant("문제를 찾았습니다. `ActionService` 생성자에서 `lastKnownSettings`를 초기화하지 않아서, 실행 직후에는 `null` 상태입니다")
	rt.printAssistant("문제를 찾았습니다. `ActionService` 생성자에서 `lastKnownSettings`를 초기화하지 않아서 실행 직후에는 `null` 상태입니다.")

	rendered := strings.TrimSpace(out.String())
	if strings.Count(rendered, ">> assistant ") != 1 {
		t.Fatalf("expected punctuation-only variant to be suppressed, got %q", rendered)
	}
}

func TestRuntimeStateAppendAssistantStreamIgnoresLeadingWhitespaceOnlyChunks(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
	}

	rt.appendAssistantStream("\n\n")
	rt.finishAssistantStream()

	if strings.TrimSpace(out.String()) != "" {
		t.Fatalf("expected whitespace-only streamed chunks to produce no output, got %q", out.String())
	}
}

func TestRuntimeStateAppendAssistantStreamTrimsLeadingWhitespaceBeforeFirstVisibleText(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
	}

	rt.appendAssistantStream("\n\nhello")
	rt.finishAssistantStream()

	rendered := out.String()
	if !strings.Contains(rendered, ">> assistant ") || !strings.Contains(rendered, "\nhello") {
		t.Fatalf("expected streamed output to start with visible text, got %q", rendered)
	}
	if strings.Contains(rendered, "\n\nhello") {
		t.Fatalf("expected no blank line immediately after assistant label, got %q", rendered)
	}
}

func TestRuntimeStateAppendAssistantStreamPreservesBufferedParagraphBreaks(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
	}

	rt.appendAssistantStream("Hello")
	rt.appendAssistantStream("\n\n")
	rt.appendAssistantStream("World")
	rt.finishAssistantStream()

	rendered := out.String()
	if !strings.Contains(rendered, "Hello\n\nWorld") {
		t.Fatalf("expected buffered paragraph break to be preserved, got %q", rendered)
	}
	if strings.Count(rendered, ">> assistant ") != 1 {
		t.Fatalf("expected a single assistant block, got %q", rendered)
	}
}

func TestRuntimeStateAppendAssistantStreamShowsWorkingStatusForRepeatedBlankLines(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		cfg: Config{
			AutoLocale: boolPtr(false),
		},
		writer: &out,
		ui:     UI{},
	}

	rt.appendAssistantStream("Applying patch")
	rt.appendAssistantStream("\n")
	rt.appendAssistantStream("\n")
	rt.appendAssistantStream("\n")
	time.Sleep(30 * time.Millisecond)
	rt.stopThinkingIndicator()

	rendered := out.String()
	if !strings.Contains(rendered, ">> assistant ") || !strings.Contains(rendered, "\nApplying patch\n") {
		t.Fatalf("expected partial assistant reply to flush before placeholder status, got %q", rendered)
	}
	if !strings.Contains(rendered, "Working ...") {
		t.Fatalf("expected working placeholder after repeated blank lines, got %q", rendered)
	}
	if strings.Contains(rendered, "\n\n\n") {
		t.Fatalf("expected repeated blank lines to be replaced with progress, got %q", rendered)
	}
}

func TestRuntimeStatePrintWhileThinkingFlushesActiveAssistantStream(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
	}

	rt.appendAssistantStream("partial reply")
	rt.printWhileThinking(rt.ui.infoLine("Creating automatic checkpoint before edit..."))

	rendered := out.String()
	if !strings.Contains(rendered, ">> assistant ") || !strings.Contains(rendered, "\npartial reply\n") {
		t.Fatalf("expected assistant stream to be terminated before info output, got %q", rendered)
	}
	if !strings.Contains(rendered, "INFO  Creating automatic checkpoint before edit...") {
		t.Fatalf("expected info line to be printed, got %q", rendered)
	}
	if !strings.Contains(rendered, "  | ") {
		t.Fatalf("expected progress output to be visually nested, got %q", rendered)
	}
	if strings.Contains(rendered, "partial replyINFO") {
		t.Fatalf("expected assistant and info output to be separated, got %q", rendered)
	}
}

func TestRuntimeStatePrintWhileThinkingDoesNotStartIndicatorWhenInactive(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
	}

	rt.printWhileThinking(rt.ui.infoLine("Creating automatic checkpoint before edit..."))

	rt.thinkingMu.Lock()
	active := rt.thinkingStop != nil
	rt.thinkingMu.Unlock()
	if active {
		rt.stopThinkingIndicator()
		t.Fatalf("expected printWhileThinking to leave the indicator stopped when it was inactive")
	}
}

func TestRuntimeStateWithPinnedPromptBlocksConcurrentOutput(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
	}

	started := make(chan struct{})
	done := make(chan struct{})
	err := rt.withPinnedPrompt(func() error {
		go func() {
			close(started)
			rt.writeOutput("stream")
			close(done)
		}()
		<-started
		select {
		case <-done:
			t.Fatalf("expected concurrent output to block while prompt is pinned")
		case <-time.After(30 * time.Millisecond):
		}
		fmt.Fprint(rt.writer, "prompt")
		return nil
	})
	if err != nil {
		t.Fatalf("withPinnedPrompt returned error: %v", err)
	}
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected blocked output to resume after pinned prompt finished")
	}
	if got := out.String(); got != "promptstream" {
		t.Fatalf("unexpected pinned prompt output ordering: %q", got)
	}
}

func TestRuntimeStateRenderFooterLineReplacesPreviousFooter(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
	}

	rt.renderFooterLine("footer status")
	rt.renderFooterLine("ok")

	want := "footer status\rok           "
	if got := out.String(); got != want {
		t.Fatalf("unexpected footer render sequence: %q", got)
	}
	if !rt.footerVisible {
		t.Fatalf("expected footer to remain visible after replacement")
	}
	if rt.footerLineCount != 1 {
		t.Fatalf("expected footer line count to track replacement text, got %d", rt.footerLineCount)
	}
}

func TestRuntimeStateWithPinnedPromptClearsFooterBeforePrompt(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
	}

	rt.renderFooterLine("footer status")

	err := rt.withPinnedPrompt(func() error {
		fmt.Fprint(rt.writer, "prompt")
		return nil
	})
	if err != nil {
		t.Fatalf("withPinnedPrompt returned error: %v", err)
	}

	want := "footer status\r             \rprompt"
	if got := out.String(); got != want {
		t.Fatalf("unexpected prompt footer sequence: %q", got)
	}
	if rt.footerVisible {
		t.Fatalf("expected footer to be cleared while prompt owns the bottom line")
	}
}

func TestRuntimeStateRenderFooterTextClearsWrappedPanel(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
	}

	rt.renderFooterText("alpha\nbeta")
	rt.renderFooterText("ok")

	want := "alpha\nbeta\x1b[1A\r\x1b[Jok"
	if got := out.String(); got != want {
		t.Fatalf("unexpected wrapped footer render sequence: %q", got)
	}
	if rt.footerLineCount != 1 {
		t.Fatalf("expected wrapped footer replacement to leave one visible line, got %d", rt.footerLineCount)
	}
}

func TestRuntimeStatePromptResolveAutoVerifyFailurePinsPromptAgainstConcurrentOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	input := &blockingLineInput{
		started:   make(chan struct{}),
		release:   make(chan struct{}),
		remaining: "y\n",
	}
	var out bytes.Buffer
	rt := &runtimeState{
		reader:      bufio.NewReader(input),
		writer:      &out,
		ui:          UI{},
		interactive: true,
		detectVerificationToolPath: func(string) string {
			return ""
		},
		cfg: DefaultConfig(home),
		session: &Session{
			Provider:       "openai",
			Model:          "gpt-test",
			BaseURL:        "https://example.test",
			PermissionMode: "default",
		},
	}
	rt.agent = &Agent{Config: rt.cfg}

	report := VerificationReport{
		Steps: []VerificationStep{{
			Label:       "msbuild demo.sln",
			Command:     "msbuild demo.sln /m",
			Status:      VerificationFailed,
			FailureKind: "command_not_found",
			Hint:        "A required verification tool could not be started.",
		}},
	}

	resultCh := make(chan AutoVerifyFailureResolution, 1)
	errCh := make(chan error, 1)
	go func() {
		resolution, err := rt.promptResolveAutoVerifyFailure(report)
		resultCh <- resolution
		errCh <- err
	}()

	select {
	case <-input.started:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected auto-verify prompt to begin reading input")
	}

	writeDone := make(chan struct{})
	go func() {
		rt.writeOutput("stream")
		close(writeDone)
	}()

	select {
	case <-writeDone:
		t.Fatal("expected concurrent output to block while auto-verify prompt is pinned")
	case <-time.After(30 * time.Millisecond):
	}

	close(input.release)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("promptResolveAutoVerifyFailure: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected auto-verify prompt to finish after input was released")
	}

	resolution := <-resultCh
	if resolution != AutoVerifyFailureDisable {
		t.Fatalf("expected disable resolution, got %q", resolution)
	}

	select {
	case <-writeDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected blocked output to resume after auto-verify prompt finished")
	}

	rendered := out.String()
	promptIndex := strings.Index(rendered, "Automatic verification failed because a verification tool could not be started.")
	streamIndex := strings.Index(rendered, "stream")
	if promptIndex == -1 {
		t.Fatalf("expected auto-verify prompt text in output, got %q", rendered)
	}
	if streamIndex == -1 {
		t.Fatalf("expected resumed output marker in output, got %q", rendered)
	}
	if streamIndex < promptIndex {
		t.Fatalf("expected prompt to appear before resumed output, got %q", rendered)
	}
}

func TestFormatAssistantErrorAddsGuidanceForToolUseUnsupportedModel(t *testing.T) {
	rt := &runtimeState{
		ui: UI{},
	}

	lines := rt.formatAssistantError(fmt.Errorf("selected model does not support tool use for inspect/edit requests: provider=openrouter model=meta-llama/llama-3.2-11b-vision-instruct"))
	if len(lines) < 2 {
		t.Fatalf("expected guidance lines, got %#v", lines)
	}
	rendered := strings.Join(lines, "\n")
	if !strings.Contains(rendered, "cannot use Kernforge tools") {
		t.Fatalf("expected tool-use guidance, got %q", rendered)
	}
	if !strings.Contains(rendered, "Alternative 1: retry the same prompt with a different tool-capable model or provider.") {
		t.Fatalf("expected alternative guidance, got %q", rendered)
	}
}

func TestRuntimeStatePromptCanceledCommandPrintsInfoNotError(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "openai", "gpt-test", "", "default")
	var out bytes.Buffer
	rt := &runtimeState{
		writer:  &out,
		ui:      UI{},
		session: session,
		store:   NewSessionStore(filepath.Join(root, "sessions")),
	}

	rt.printCommandExecutionError("/model cross-review", ErrPromptCanceled)

	rendered := out.String()
	if !strings.Contains(rendered, "INFO  Canceled.") {
		t.Fatalf("expected cancellation to render as info, got %q", rendered)
	}
	for _, banned := range []string{"ERROR", "error", "prompt canceled", "command error"} {
		if strings.Contains(rendered, banned) {
			t.Fatalf("cancellation output should not contain %q, got %q", banned, rendered)
		}
	}
	if len(session.ConversationEvents) != 0 {
		t.Fatalf("prompt cancellation should not be recorded as a command event, got %#v", session.ConversationEvents)
	}
}

func TestRuntimeStateRequestCanceledCommandPrintsInfoNotError(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "openai", "gpt-test", "", "default")
	var out bytes.Buffer
	rt := &runtimeState{
		writer:  &out,
		ui:      UI{},
		session: session,
		store:   NewSessionStore(filepath.Join(root, "sessions")),
	}

	rt.printCommandExecutionError("/review", ErrRequestCanceled)

	rendered := out.String()
	if !strings.Contains(rendered, "INFO  Request canceled.") {
		t.Fatalf("expected request cancellation to render as info, got %q", rendered)
	}
	for _, banned := range []string{"ERROR", "command error", "request canceled by user"} {
		if strings.Contains(rendered, banned) {
			t.Fatalf("request cancellation output should not contain %q, got %q", banned, rendered)
		}
	}
	if len(session.ConversationEvents) != 0 {
		t.Fatalf("request cancellation should not be recorded as a command event, got %#v", session.ConversationEvents)
	}
}

func TestFormatAssistantErrorTreatsRequestCancelAsInfoNotError(t *testing.T) {
	rt := &runtimeState{
		ui: UI{},
	}

	lines := rt.formatAssistantError(ErrRequestCanceled)
	rendered := strings.Join(lines, "\n")
	if !strings.Contains(rendered, "INFO  Request canceled.") {
		t.Fatalf("expected request cancellation to render as info, got %q", rendered)
	}
	for _, banned := range []string{"ERROR", "error", "assistant error", "request canceled by user"} {
		if strings.Contains(rendered, banned) {
			t.Fatalf("request cancellation output should not contain %q, got %q", banned, rendered)
		}
	}
}

func TestCodexRefreshTokenReuseIsAuthError(t *testing.T) {
	err := fmt.Errorf("openai-codex API error (400 Bad Request): Your refresh token has already been used to generate a new access token. Please try signing in again. | type=invalid_request_error | code=refresh_token_reused")
	if !isAuthError(err) {
		t.Fatalf("expected refresh token reuse to be classified as auth error")
	}

	rt := &runtimeState{ui: UI{}}
	rendered := strings.Join(rt.formatAssistantError(err), "\n")
	if !strings.Contains(rendered, "/codex-auth login") {
		t.Fatalf("expected codex auth recovery hint, got %q", rendered)
	}
}

func TestCodexAuthErrorWarnsLoginRecovery(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "openai-codex", "gpt-5.5", "", "default")
	var out bytes.Buffer
	rt := &runtimeState{
		writer:  &out,
		ui:      UI{},
		session: session,
	}

	if err := rt.handleAuthError(); err != nil {
		t.Fatalf("handleAuthError: %v", err)
	}
	rendered := out.String()
	for _, want := range []string{"/codex-auth login", "/codex-auth logout", "refresh token"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected auth warning to contain %q, got %q", want, rendered)
		}
	}
}

func TestEnsureOpenAICodexAuthInteractiveImportsCodexCLIAuth(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgForTrust := DefaultConfig(workspace)
	markConfigProjectTrustedForTest(t, &cfgForTrust, workspace)
	if err := SaveUserConfig(cfgForTrust); err != nil {
		t.Fatalf("SaveUserConfig: %v", err)
	}

	if err := saveCodexOAuthAuthFile(codexCLIOAuthAuthFilePath(), codexOAuthTokens{
		AccessToken:  testCodexOAuthJWT(time.Now().Add(time.Hour)),
		RefreshToken: "refresh-1",
		AccountID:    "account-1",
	}); err != nil {
		t.Fatalf("save Codex CLI auth: %v", err)
	}

	var out bytes.Buffer
	rt := &runtimeState{
		cfg:         DefaultConfig(workspace),
		reader:      bufio.NewReader(strings.NewReader("y\n")),
		writer:      &out,
		ui:          UI{},
		interactive: true,
	}

	if err := rt.ensureOpenAICodexAuthInteractive(); err != nil {
		t.Fatalf("ensureOpenAICodexAuthInteractive: %v", err)
	}
	if !codexOAuthAuthFileUsable(codexOAuthAuthFilePath()) {
		t.Fatalf("expected Kernforge auth file to be usable after Codex CLI import")
	}
	rendered := out.String()
	if !strings.Contains(rendered, "Existing Codex CLI OAuth login detected") ||
		!strings.Contains(rendered, "OpenAI Codex OAuth imported from Codex CLI login") {
		t.Fatalf("expected import UX, got %q", rendered)
	}
}

func TestFormatAssistantErrorAddsGuidanceForEmptyResponseAfterTool(t *testing.T) {
	rt := &runtimeState{
		ui: UI{},
	}

	lines := rt.formatAssistantError(fmt.Errorf("model returned an empty response (provider=openrouter model=openai/gpt-oss-120b stop_reason=stream_empty_fallback_empty_after_stream_retry after_tool=true)"))
	if len(lines) < 2 {
		t.Fatalf("expected guidance lines, got %#v", lines)
	}
	rendered := strings.Join(lines, "\n")
	if !strings.Contains(rendered, "provider returned no visible assistant text") {
		t.Fatalf("expected empty-response guidance, got %q", rendered)
	}
	if !strings.Contains(rendered, "streamed reply was empty") {
		t.Fatalf("expected stream fallback guidance, got %q", rendered)
	}
	if !strings.Contains(rendered, "after at least one successful tool result") {
		t.Fatalf("expected after-tool guidance, got %q", rendered)
	}
	if !strings.Contains(rendered, "Alternative 2: ask for a no-tools answer") {
		t.Fatalf("expected alternative guidance, got %q", rendered)
	}
}

func TestSessionApprovalStateLabel(t *testing.T) {
	if got := sessionApprovalStateLabel(false, "auto-approve"); got != "ask" {
		t.Fatalf("unexpected disabled label: %q", got)
	}
	if got := sessionApprovalStateLabel(true, "auto-approve"); got != "auto-approve (session)" {
		t.Fatalf("unexpected enabled label: %q", got)
	}
}

func TestRuntimeStatePrintAssistantFlushesActiveAssistantStream(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
	}

	rt.appendAssistantStream("partial reply")
	rt.printAssistant("Final answer")

	rendered := out.String()
	if strings.Count(rendered, ">> assistant ") != 2 {
		t.Fatalf("expected two assistant blocks, got %q", rendered)
	}
	if !strings.Contains(rendered, "partial reply\n>> assistant ") || !strings.Contains(rendered, "\nFinal answer") {
		t.Fatalf("expected assistant stream to flush before final assistant output, got %q", rendered)
	}
}

func TestRuntimeStatePrintAssistantWhileThinkingUsesFooterPanelWhenInteractive(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer:      &out,
		ui:          UI{},
		cfg:         Config{ProgressDisplay: "compact"},
		interactive: true,
	}

	rt.printAssistantWhileThinking("I am going to inspect the auth flow first.")
	defer rt.stopThinkingIndicator()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		rendered := out.String()
		if strings.Contains(rendered, "[thinking]") {
			if !strings.Contains(rendered, "[next") {
				t.Fatalf("expected assistant preamble to render in footer panel, got %q", rendered)
			}
			if strings.Contains(rendered, "  | ") {
				t.Fatalf("expected interactive assistant preamble to avoid persistent progress lines, got %q", rendered)
			}
			if rt.footerLineCount < 2 {
				t.Fatalf("expected next-step footer panel to occupy multiple lines, got %d", rt.footerLineCount)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("expected footer panel for assistant preamble, got %q", out.String())
}

func TestRuntimeStatePrintAssistantWhileThinkingFallsBackToProgressLineWhenNonInteractive(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
	}

	rt.printAssistantWhileThinking("I am going to inspect the auth flow first.")

	rendered := out.String()
	if !strings.Contains(rendered, "[next") {
		t.Fatalf("expected non-interactive assistant preamble to render as next-step activity, got %q", rendered)
	}
	if !strings.Contains(rendered, "  | ") {
		t.Fatalf("expected non-interactive assistant preamble to use persistent progress lines, got %q", rendered)
	}
}

func TestPrintProgressMessageClassifiesEditEvents(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
	}

	rt.printProgressMessage("Creating automatic checkpoint before edit...")

	rendered := out.String()
	if !strings.Contains(rendered, "[edit") {
		t.Fatalf("expected checkpoint progress to use edit activity badge, got %q", rendered)
	}
	if !strings.Contains(rendered, "Creating automatic checkpoint before edit...") {
		t.Fatalf("expected progress text to be preserved, got %q", rendered)
	}
}

func TestPrintProgressMessageClassifiesToolEvents(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
	}

	rt.printProgressMessage("read_file loaded main.go (12 line(s)).")

	rendered := out.String()
	if !strings.Contains(rendered, "[tool") {
		t.Fatalf("expected read_file completion to use tool badge, got %q", rendered)
	}
	if !strings.Contains(rendered, "read_file loaded main.go (12 line(s)).") {
		t.Fatalf("expected tool completion text to be preserved, got %q", rendered)
	}
}

func TestPrintProgressMessageUsesFooterWhenInteractive(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer:      &out,
		ui:          UI{},
		cfg:         Config{ProgressDisplay: "compact"},
		interactive: true,
	}

	rt.printProgressMessage("Creating automatic checkpoint before edit...")
	defer rt.stopThinkingIndicator()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		rendered := out.String()
		if strings.Contains(rendered, "[thinking]") {
			if strings.Contains(rendered, "  | ") {
				t.Fatalf("expected interactive progress to use footer instead of persistent progress lines, got %q", rendered)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("expected interactive progress to render via the footer, got %q", out.String())
}

func TestPrintProgressMessageAutoPersistsToolEventsWhenInteractive(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer:      &out,
		ui:          UI{},
		cfg:         Config{ProgressDisplay: "auto"},
		interactive: true,
	}

	rt.printProgressMessage("read_file loaded main.go (12 line(s)).")

	rendered := out.String()
	if !strings.Contains(rendered, "[tool") {
		t.Fatalf("expected auto progress display to persist tool activity, got %q", rendered)
	}
	if !strings.Contains(rendered, "  | ") {
		t.Fatalf("expected auto progress display to use persistent progress line, got %q", rendered)
	}
	if rt.footerVisible {
		t.Fatalf("expected persistent tool activity to avoid transient footer")
	}
}

func TestPrintProgressMessageCompactKeepsToolEventsInFooter(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer:      &out,
		ui:          UI{},
		cfg:         Config{ProgressDisplay: "compact"},
		interactive: true,
	}

	rt.printProgressMessage("read_file loaded main.go (12 line(s)).")
	defer rt.stopThinkingIndicator()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		rendered := out.String()
		if strings.Contains(rendered, "[thinking]") {
			if strings.Contains(rendered, "  | ") {
				t.Fatalf("expected compact progress display to keep tool activity in footer, got %q", rendered)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("expected compact progress display to render footer, got %q", out.String())
}

func TestPrintProgressMessageInteractiveFlushesAssistantStreamBeforeFooter(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer:      &out,
		ui:          UI{},
		cfg:         Config{ProgressDisplay: "compact"},
		interactive: true,
	}

	rt.appendAssistantStream("partial reply")
	rt.printProgressMessage("read_file loaded main.go (12 line(s)).")
	defer rt.stopThinkingIndicator()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		rendered := out.String()
		if strings.Contains(rendered, "[thinking]") {
			if !strings.Contains(rendered, "partial reply\n") {
				t.Fatalf("expected assistant stream to flush before footer progress, got %q", rendered)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("expected footer progress after assistant flush, got %q", out.String())
}

func TestShowTransientPanelWhileThinkingRendersMultilineFooter(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer:      &out,
		ui:          UI{},
		interactive: true,
	}

	if !rt.showTransientPanelWhileThinking("INFO  analysis wave 1/3", "TIP  shard 4/12") {
		t.Fatalf("expected transient panel to be enabled in interactive mode")
	}
	defer rt.stopThinkingIndicator()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		rendered := out.String()
		if strings.Contains(rendered, "[thinking]") && strings.Contains(rendered, "analysis wave 1/3") {
			if !strings.Contains(rendered, "TIP  shard 4/12") {
				t.Fatalf("expected multiline footer details, got %q", rendered)
			}
			if rt.footerLineCount < 3 {
				t.Fatalf("expected footer panel to occupy multiple lines, got %d", rt.footerLineCount)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("expected multiline transient footer panel, got %q", out.String())
}

func TestPrintPersistentWhileThinkingKeepsResultsInBodyAfterFooterState(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer:      &out,
		ui:          UI{},
		interactive: true,
	}

	if !rt.showTransientWhileThinking("INFO  analysis wave 1/3", "TIP  shard 4/12") {
		t.Fatalf("expected transient footer state to be enabled")
	}
	time.Sleep(30 * time.Millisecond)
	rt.printPersistentWhileThinking(rt.ui.successLine("Analysis completed with 12 shard(s)."))
	rt.stopThinkingIndicator()

	rendered := out.String()
	if !strings.Contains(rendered, "OK  Analysis completed with 12 shard(s).") {
		t.Fatalf("expected result summary to remain in the body output, got %q", rendered)
	}
	if !strings.Contains(rendered, "analysis wave 1/3") {
		t.Fatalf("expected prior transient footer state to render before the result summary, got %q", rendered)
	}
	if rt.footerVisible {
		t.Fatalf("expected persistent body output to clear the transient footer")
	}
}

func TestAssistantStreamClearsNextStepFooterPanel(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer:      &out,
		ui:          UI{},
		interactive: true,
	}

	rt.printAssistantWhileThinking("I am going to inspect the auth flow first.")
	time.Sleep(30 * time.Millisecond)
	rt.appendAssistantStream("Actual answer")
	rt.finishAssistantStream()
	rt.stopThinkingIndicator()

	rendered := out.String()
	if !strings.Contains(rendered, ">> assistant ") || !strings.Contains(rendered, "Actual answer") {
		t.Fatalf("expected streamed assistant output after footer preamble, got %q", rendered)
	}
	if rt.footerVisible {
		t.Fatalf("expected footer panel to be cleared once assistant streaming begins")
	}
}

func TestRuntimeStateAppendAssistantStreamInsertsBreakBeforeFollowOnPreamble(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
	}

	rt.appendAssistantStream("Let me read AnthropicProvider and GeminiProvider:")
	rt.appendAssistantStream("Now I have all the files.")
	rt.finishAssistantStream()

	rendered := out.String()
	if !strings.Contains(rendered, "GeminiProvider:\nNow I have all the files.") {
		t.Fatalf("expected follow-on preamble to be separated onto a new line, got %q", rendered)
	}
}

func TestRuntimeStateAppendAssistantStreamDoesNotBreakNormalContinuation(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
	}

	rt.appendAssistantStream("This is a sentence")
	rt.appendAssistantStream(" continuation.")
	rt.finishAssistantStream()

	rendered := out.String()
	if strings.Contains(rendered, "sentence\n continuation") {
		t.Fatalf("expected normal continuation to stay on the same line, got %q", rendered)
	}
}

func TestRuntimeStateAppendAssistantStreamFormatsListBoundaryAcrossChunks(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
	}

	rt.appendAssistantStream("Summary:")
	rt.appendAssistantStream("\n- first\n- second")
	rt.finishAssistantStream()

	rendered := out.String()
	if !strings.Contains(rendered, "Summary:\n\n- first\n- second") {
		t.Fatalf("expected streamed output to insert paragraph spacing before list, got %q", rendered)
	}
}

func TestRuntimeStateAppendAssistantStreamUsesCodeToneInsideFence(t *testing.T) {
	var out bytes.Buffer
	ui := UI{color: true}
	rt := &runtimeState{
		writer: &out,
		ui:     ui,
	}

	rt.appendAssistantStream("Summary\n")
	rt.appendAssistantStream("```go\n")
	rt.appendAssistantStream("fmt.Println(\"hi\")\n")
	rt.appendAssistantStream("```\n")
	rt.finishAssistantStream()

	rendered := out.String()
	if !strings.Contains(rendered, ui.mint("Summary")) {
		t.Fatalf("expected paragraph text to keep assistant body tone, got %q", rendered)
	}
	if !strings.Contains(rendered, ui.assistantCode("```go")) {
		t.Fatalf("expected fence line to use code tone during streaming, got %q", rendered)
	}
	if !strings.Contains(rendered, ui.assistantCode("fmt.Println(\"hi\")")) {
		t.Fatalf("expected code line to use code tone during streaming, got %q", rendered)
	}
}

func TestParseToolLoopDiagnosticsExtractsFields(t *testing.T) {
	toolSummary, stopReason, recentTurns := parseToolLoopDiagnostics("tool loop limit exceeded (last_tools=list_files, run_shell; stop_reason=tool_calls; iteration=16; max_iterations=16; recent_turns=list_files:ok | run_shell:error)")
	if toolSummary != "list_files, run_shell" {
		t.Fatalf("expected tool diagnostic field, got %q", toolSummary)
	}
	if stopReason != "tool_calls" {
		t.Fatalf("expected stop reason, got %q", stopReason)
	}
	if recentTurns != "list_files:ok | run_shell:error" {
		t.Fatalf("expected recent turns, got %q", recentTurns)
	}
}

func TestRuntimeStateFormatAssistantErrorForToolLoop(t *testing.T) {
	rt := &runtimeState{
		ui: UI{},
	}

	lines := rt.formatAssistantError(fmt.Errorf("tool loop limit exceeded (last_tools=list_files; stop_reason=tool_calls; iteration=16; max_iterations=16; recent_turns=list_files:ok | run_shell:error)"))
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "assistant error: tool loop limit exceeded") {
		t.Fatalf("expected base error line, got %q", joined)
	}
	if !strings.Contains(joined, "Last tool sequence: list_files") {
		t.Fatalf("expected tool summary hint, got %q", joined)
	}
	if !strings.Contains(joined, "Model stop reason: tool_calls") {
		t.Fatalf("expected stop reason hint, got %q", joined)
	}
	if !strings.Contains(joined, "Recent tool turns: list_files:ok | run_shell:error") {
		t.Fatalf("expected recent tool turns hint, got %q", joined)
	}
}

func TestRuntimeStateFormatAssistantErrorForTokenLimit(t *testing.T) {
	rt := &runtimeState{
		ui: UI{},
	}

	lines := rt.formatAssistantError(fmt.Errorf("model stopped before producing a usable response due to token limit (stop_reason=length)"))
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Model stop reason: length") {
		t.Fatalf("expected stop reason hint, got %q", joined)
	}
	if !strings.Contains(joined, "Increase max_tokens") {
		t.Fatalf("expected token limit guidance, got %q", joined)
	}
}

func TestRuntimeStateFormatAssistantErrorForDeadlineExceeded(t *testing.T) {
	rt := &runtimeState{
		ui: UI{},
	}

	lines := rt.formatAssistantError(context.DeadlineExceeded)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "assistant error: context deadline exceeded") {
		t.Fatalf("expected base timeout error line, got %q", joined)
	}
	if !strings.Contains(joined, "timed out before a usable response arrived") {
		t.Fatalf("expected timeout guidance, got %q", joined)
	}
}

func TestParseRejectedEditTargetPath(t *testing.T) {
	text := "stopped after repeated tool failure: edit target mismatch: refusing to edit nested worktree-managed path outside the active workspace root: C:\\git\\kernforge\\.claude\\worktrees\\compassionate-goldberg\\completion.go"
	path := parseRejectedEditTargetPath(text)
	if path != "C:\\git\\kernforge\\.claude\\worktrees\\compassionate-goldberg\\completion.go" {
		t.Fatalf("unexpected rejected path: %q", path)
	}
}

func TestRuntimeStateFormatAssistantErrorForEditTargetMismatch(t *testing.T) {
	rt := &runtimeState{
		ui: UI{},
		session: &Session{
			WorkingDir: "C:\\git\\kernforge",
		},
	}

	lines := rt.formatAssistantError(fmt.Errorf("stopped after repeated tool failure: edit target mismatch: search text not found in C:\\git\\kernforge\\.claude\\worktrees\\compassionate-goldberg\\completion.go"))
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Active workspace root: C:\\git\\kernforge") {
		t.Fatalf("expected workspace root hint, got %q", joined)
	}
	if !strings.Contains(joined, "Rejected target path: C:\\git\\kernforge\\.claude\\worktrees\\compassionate-goldberg\\completion.go") {
		t.Fatalf("expected rejected path hint, got %q", joined)
	}
	if !strings.Contains(joined, "Re-read the file from the exact same path before editing again") {
		t.Fatalf("expected rerun guidance, got %q", joined)
	}
}

func TestRuntimeStateHandleSetAutoVerifyCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	output := &bytes.Buffer{}
	rt := &runtimeState{
		writer: output,
		ui:     UI{},
		cfg:    DefaultConfig(home),
		session: &Session{
			Provider:       "openai",
			Model:          "gpt-test",
			BaseURL:        "https://example.test",
			PermissionMode: "default",
		},
		agent: &Agent{
			Config: DefaultConfig(home),
		},
	}

	if err := rt.handleSetAutoVerifyCommand("off"); err != nil {
		t.Fatalf("handleSetAutoVerifyCommand: %v", err)
	}
	if configAutoVerify(rt.cfg) {
		t.Fatalf("expected auto_verify to be disabled in runtime config")
	}
	if configAutoVerify(rt.agent.Config) {
		t.Fatalf("expected auto_verify to be disabled in agent runtime config")
	}
	if !strings.Contains(output.String(), "Automatic verification set to false") {
		t.Fatalf("expected success output, got %q", output.String())
	}

	saved, err := LoadConfig(home)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if configAutoVerify(saved) {
		t.Fatalf("expected saved config to keep auto_verify disabled")
	}
}

func TestRuntimeStateHandleModelCommandRejectsUnknownTarget(t *testing.T) {
	rt := &runtimeState{}
	err := rt.handleModelCommand("gpt-5.4")
	if err == nil {
		t.Fatalf("expected /model to reject an unknown target")
	}
	if !strings.Contains(err.Error(), "unknown model target") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuntimeStateHandleModelCommandShowsAllRoutingNonInteractive(t *testing.T) {
	var out bytes.Buffer
	cfg := DefaultConfig(t.TempDir())
	cfg.Provider = "openai"
	cfg.Model = "gpt-main"
	cfg.ProjectAnalysis.WorkerProfile = &Profile{
		Provider: "anthropic",
		Model:    "claude-worker",
	}
	cfg.ProjectAnalysis.ReviewerProfile = &Profile{
		Provider: "openai",
		Model:    "gpt-analysis-review",
	}
	cfg.Specialists.Profiles = []SpecialistSubagentProfile{{
		Name:     "planner",
		Provider: "openai",
		Model:    "gpt-5.4-mini",
	}, {
		Name: "unreal-integrity-analyst",
	}, {
		Name: "memory-inspection-analyst",
	}}
	rt := &runtimeState{
		writer:  &out,
		ui:      UI{},
		cfg:     cfg,
		session: &Session{Provider: "openai", Model: "gpt-main"},
	}

	if err := rt.handleModelCommand(""); err != nil {
		t.Fatalf("handleModelCommand: %v", err)
	}

	rendered := out.String()
	for _, needle := range []string{
		"Model Routing",
		"main",
		"analysis_worker",
		"analysis_reviewer",
		"cross_review",
		"primary /review route follows main",
		"use /model cross-review",
		"Task Owner Model Overrides",
		"not /review routes",
		"planner",
		"gpt-5.4-mini",
	} {
		if !strings.Contains(rendered, needle) {
			t.Fatalf("expected model routing output to contain %q, got %q", needle, rendered)
		}
	}
	for _, bad := range []string{
		"unreal-integrity-analyst:",
		"memory-inspection-analyst:",
		"not configured; follows main model -> openai-api / gpt-main",
	} {
		if strings.Contains(rendered, bad) {
			t.Fatalf("expected unconfigured owner model routes to stay hidden, got %q", rendered)
		}
	}
	for _, bad := range []string{
		"unreal-integrity-analyst ->",
		"memory-inspection-analyst ->",
	} {
		if strings.Contains(rendered, bad) {
			t.Fatalf("expected long specialist names to stay aligned, got %q", rendered)
		}
	}
}

func TestRuntimeStateHandleModelCommandInteractiveRoutesToAnalysisReviewer(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var out bytes.Buffer
	cfg := DefaultConfig(workspace)
	cfg.Provider = "openai"
	cfg.Model = "gpt-main"
	cfg.APIKey = "test-key"
	rt := &runtimeState{
		reader:      bufio.NewReader(strings.NewReader("3\n3\n1\n")),
		writer:      &out,
		ui:          UI{},
		interactive: true,
		cfg:         cfg,
		session: &Session{
			Provider:       "openai",
			Model:          "gpt-main",
			BaseURL:        "https://example.test",
			PermissionMode: "default",
		},
	}

	if err := rt.handleModelCommand(""); err != nil {
		t.Fatalf("handleModelCommand: %v", err)
	}
	if rt.cfg.ProjectAnalysis.ReviewerProfile == nil {
		t.Fatalf("expected analysis reviewer profile to be configured")
	}
	if rt.cfg.ProjectAnalysis.ReviewerProfile.Provider != "openai" || rt.cfg.ProjectAnalysis.ReviewerProfile.Model != "gpt-5.4" {
		t.Fatalf("unexpected analysis reviewer profile: %#v", rt.cfg.ProjectAnalysis.ReviewerProfile)
	}
}

func TestRuntimeStateHandleSetSpecialistModelCommandPersistsWorkspaceOverride(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgForTrust := DefaultConfig(workspace)
	markConfigProjectTrustedForTest(t, &cfgForTrust, workspace)
	if err := SaveUserConfig(cfgForTrust); err != nil {
		t.Fatalf("SaveUserConfig: %v", err)
	}

	output := &bytes.Buffer{}
	rt := &runtimeState{
		writer: output,
		ui:     UI{},
		cfg:    DefaultConfig(workspace),
		session: &Session{
			Provider:       "openai",
			Model:          "gpt-main",
			BaseURL:        "https://example.test",
			PermissionMode: "default",
		},
		workspace: Workspace{
			BaseRoot: workspace,
			Root:     workspace,
		},
	}
	rt.cfg.Provider = "openai"
	rt.cfg.Model = "gpt-main"
	rt.agent = &Agent{
		Config: rt.cfg,
	}

	if err := rt.handleSetSpecialistModelCommand("planner openai gpt-5.4-mini"); err != nil {
		t.Fatalf("handleSetSpecialistModelCommand: %v", err)
	}
	if !strings.Contains(output.String(), "Task owner planner model override set: openai-api / gpt-5.4-mini") {
		t.Fatalf("expected success output, got %q", output.String())
	}
	profile, ok := configuredSpecialistProfileByName(rt.cfg, "planner")
	if !ok {
		t.Fatalf("expected planner profile after override")
	}
	if profile.Provider != "openai" || profile.Model != "gpt-5.4-mini" {
		t.Fatalf("expected runtime override to apply, got %#v", profile)
	}
	if len(rt.agent.Config.Specialists.Profiles) == 0 {
		t.Fatalf("expected agent config to receive specialist overrides")
	}

	saved, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	savedProfile, ok := configuredSpecialistProfileByName(saved, "planner")
	if !ok {
		t.Fatalf("expected saved planner profile")
	}
	if savedProfile.Provider != "openai" || savedProfile.Model != "gpt-5.4-mini" {
		t.Fatalf("expected saved specialist override, got %#v", savedProfile)
	}
}

func TestRuntimeStateHandleSetSpecialistModelCommandClearPreservesOtherOverrides(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgForTrust := DefaultConfig(workspace)
	markConfigProjectTrustedForTest(t, &cfgForTrust, workspace)
	if err := SaveUserConfig(cfgForTrust); err != nil {
		t.Fatalf("SaveUserConfig: %v", err)
	}

	cfg := DefaultConfig(workspace)
	cfg.Provider = "openai"
	cfg.Model = "gpt-main"
	cfg.Specialists.Profiles = []SpecialistSubagentProfile{{
		Name:      "planner",
		Prompt:    "Keep the planning prompt override.",
		Provider:  "openai",
		Model:     "gpt-5.4-mini",
		ReadOnly:  boolPtr(true),
		Editable:  boolPtr(false),
		NodeKinds: []string{"task"},
	}}

	rt := &runtimeState{
		writer: &bytes.Buffer{},
		ui:     UI{},
		cfg:    cfg,
		session: &Session{
			Provider:       "openai",
			Model:          "gpt-main",
			BaseURL:        "https://example.test",
			PermissionMode: "default",
		},
		workspace: Workspace{
			BaseRoot: workspace,
			Root:     workspace,
		},
		agent: &Agent{
			Config: cfg,
		},
	}

	if err := rt.persistSpecialistOverrides(); err != nil {
		t.Fatalf("persistSpecialistOverrides: %v", err)
	}
	if err := rt.handleSetSpecialistModelCommand("clear planner"); err != nil {
		t.Fatalf("clear planner: %v", err)
	}

	profile, ok := configuredSpecialistProfileByName(rt.cfg, "planner")
	if !ok {
		t.Fatalf("expected planner profile after clear")
	}
	if profile.Provider != "" || profile.Model != "" {
		t.Fatalf("expected planner model override to clear, got %#v", profile)
	}
	if profile.Prompt != "Keep the planning prompt override." {
		t.Fatalf("expected non-model override to be preserved, got %#v", profile)
	}

	saved, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	savedProfile, ok := configuredSpecialistProfileByName(saved, "planner")
	if !ok {
		t.Fatalf("expected saved planner profile after clear")
	}
	if savedProfile.Provider != "" || savedProfile.Model != "" {
		t.Fatalf("expected saved planner model override to clear, got %#v", savedProfile)
	}
	if savedProfile.Prompt != "Keep the planning prompt override." {
		t.Fatalf("expected saved prompt override to remain, got %#v", savedProfile)
	}
}

func TestHandleProfileCommandListsWithoutImplicitActivation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var output bytes.Buffer
	rt := &runtimeState{
		cfg:    DefaultConfig(t.TempDir()),
		ui:     NewUI(),
		writer: &output,
		session: &Session{
			ID:       "session-profile-list",
			Provider: "openai",
			Model:    "gpt-current",
		},
		interactive: false,
	}
	rt.cfg.Provider = "openai"
	rt.cfg.Model = "gpt-current"
	rt.cfg.Profiles = []Profile{
		{Name: "first", Provider: "ollama", Model: "llama3"},
		{Name: "current", Provider: "openai", Model: "gpt-current"},
	}

	if err := rt.handleProfileCommand(""); err != nil {
		t.Fatalf("handleProfileCommand: %v", err)
	}
	if rt.session.Provider != "openai" || rt.session.Model != "gpt-current" {
		t.Fatalf("expected profile list to avoid implicit activation, got %s/%s", rt.session.Provider, rt.session.Model)
	}
	if strings.Contains(output.String(), "Activated profile") {
		t.Fatalf("expected list-only output, got %q", output.String())
	}
}

func TestHandleProfileCommandAcceptsDirectActionArgs(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var output bytes.Buffer
	rt := &runtimeState{
		cfg:    DefaultConfig(workspace),
		ui:     NewUI(),
		writer: &output,
		store:  NewSessionStore(filepath.Join(home, "sessions")),
		session: &Session{
			ID:       "session-profile-activate",
			Provider: "openai",
			Model:    "gpt-current",
		},
		interactive: false,
	}
	rt.cfg.Provider = "openai"
	rt.cfg.Model = "gpt-current"
	rt.cfg.Profiles = []Profile{
		{Name: "current", Provider: "openai", Model: "gpt-current"},
		{Name: "local", Provider: "ollama", Model: "llama3", BaseURL: "http://localhost:11434"},
	}

	if err := rt.handleProfileCommand("2"); err != nil {
		t.Fatalf("handleProfileCommand activate: %v", err)
	}
	if rt.session.Provider != "ollama" || rt.session.Model != "llama3" {
		t.Fatalf("expected direct profile activation, got %s/%s", rt.session.Provider, rt.session.Model)
	}
	if !strings.Contains(output.String(), "Activated profile local") {
		t.Fatalf("expected activation output, got %q", output.String())
	}
}

func TestHandleProfileCommandAutoSavesCurrentWhenListIsEmpty(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var output bytes.Buffer
	rt := &runtimeState{
		cfg:    DefaultConfig(workspace),
		ui:     NewUI(),
		writer: &output,
		session: &Session{
			ID:       "session-profile-auto-save-current",
			Provider: "openai",
			Model:    "gpt-current",
		},
		interactive: false,
	}

	if err := rt.handleProfileCommand(""); err != nil {
		t.Fatalf("handleProfileCommand: %v", err)
	}
	if len(rt.cfg.Profiles) != 1 {
		t.Fatalf("expected one auto-saved profile, got %#v", rt.cfg.Profiles)
	}
	if rt.cfg.Profiles[0].Name != "openai-api / gpt-current" {
		t.Fatalf("unexpected auto-saved profile: %#v", rt.cfg.Profiles[0])
	}
	if !strings.Contains(output.String(), "Saved current provider/model as profile openai-api / gpt-current") {
		t.Fatalf("expected auto-save output, got %q", output.String())
	}
	if !strings.Contains(output.String(), "Profiles") {
		t.Fatalf("expected profile list output, got %q", output.String())
	}
}

func TestActivateProviderAppendsProfileForDifferentModel(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	rt := &runtimeState{
		cfg:    DefaultConfig(workspace),
		ui:     NewUI(),
		writer: &bytes.Buffer{},
		store:  NewSessionStore(filepath.Join(home, "sessions")),
		session: &Session{
			ID:       "session-profile-model-append",
			Provider: "openai",
			Model:    "gpt-old",
		},
		interactive: false,
	}
	rt.cfg.Provider = "openai"
	rt.cfg.Model = "gpt-old"
	rt.cfg.Profiles = []Profile{
		{Name: "openai / gpt-old", Provider: "openai", Model: "gpt-old", APIKey: "openai-key"},
	}
	rt.cfg.ProviderKeys = map[string]string{"openai": "openai-key"}

	if err := rt.activateProvider("openai", "gpt-new", ""); err != nil {
		t.Fatalf("activateProvider: %v", err)
	}
	if len(rt.cfg.Profiles) != 2 {
		t.Fatalf("expected model change to append a profile, got %#v", rt.cfg.Profiles)
	}
	if rt.cfg.Profiles[0].Model != "gpt-new" || rt.cfg.Profiles[1].Model != "gpt-old" {
		t.Fatalf("expected newest profile first and old profile preserved, got %#v", rt.cfg.Profiles)
	}

	saved, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(saved.Profiles) != 2 {
		t.Fatalf("expected saved profiles to preserve both models, got %#v", saved.Profiles)
	}
}

func TestActivateProviderDefaultsUndefinedReasoningEffortForEffortProvider(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var output bytes.Buffer
	rt := &runtimeState{
		cfg:    DefaultConfig(workspace),
		ui:     UI{},
		writer: &output,
		store:  NewSessionStore(filepath.Join(home, "sessions")),
		session: &Session{
			ID:       "session-effort-default-main",
			Provider: "openai",
			Model:    "gpt-old",
		},
		interactive: false,
	}
	rt.cfg.Provider = "openai"
	rt.cfg.Model = "gpt-old"

	if err := rt.activateProvider("openai-codex", "gpt-5.5", ""); err != nil {
		t.Fatalf("activateProvider: %v", err)
	}
	if rt.cfg.ReasoningEffort != "medium" {
		t.Fatalf("expected reasoning effort to default to medium, got %q", rt.cfg.ReasoningEffort)
	}
	if !strings.Contains(output.String(), "defaulted to medium") {
		t.Fatalf("expected default effort notice, got %q", output.String())
	}
	saved, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if saved.ReasoningEffort != "medium" {
		t.Fatalf("expected saved reasoning effort medium, got %q", saved.ReasoningEffort)
	}
}

func TestActivateProviderRestoresProfileReasoningEffort(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	rt := &runtimeState{
		cfg:    DefaultConfig(workspace),
		ui:     UI{},
		writer: &bytes.Buffer{},
		store:  NewSessionStore(filepath.Join(home, "sessions")),
		session: &Session{
			ID:       "session-effort-preserve-main",
			Provider: "openai",
			Model:    "gpt-old",
		},
		interactive: false,
	}
	rt.cfg.Provider = "openai"
	rt.cfg.Model = "gpt-old"
	rt.cfg.Profiles = []Profile{{
		Name:            "codex-high",
		Provider:        "openai-codex",
		Model:           "gpt-5.5",
		BaseURL:         normalizeOpenAICodexBaseURL(""),
		ReasoningEffort: "high",
	}}

	if err := rt.activateProvider("openai-codex", "gpt-5.5", ""); err != nil {
		t.Fatalf("activateProvider: %v", err)
	}
	if rt.cfg.ReasoningEffort != "high" {
		t.Fatalf("expected profile reasoning effort to be restored, got %q", rt.cfg.ReasoningEffort)
	}
}

func TestActivateProviderDoesNotChangeExplicitRoleModels(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	rt := &runtimeState{
		cfg:    DefaultConfig(workspace),
		ui:     NewUI(),
		writer: &bytes.Buffer{},
		store:  NewSessionStore(filepath.Join(home, "sessions")),
		session: &Session{
			ID:       "session-main-only-model-change",
			Provider: "openai",
			Model:    "gpt-main-old",
		},
		interactive: false,
	}
	rt.cfg.Provider = "openai"
	rt.cfg.Model = "gpt-main-old"
	rt.cfg.ProviderKeys = map[string]string{
		"openai":     "openai-key",
		"anthropic":  "anthropic-key",
		"openrouter": "openrouter-key",
	}
	rt.cfg.ProjectAnalysis.WorkerProfile = &Profile{Name: "worker", Provider: "openrouter", Model: "worker-model", APIKey: "openrouter-key"}
	rt.cfg.ProjectAnalysis.ReviewerProfile = &Profile{Name: "reviewer", Provider: "openai", Model: "analysis-reviewer", APIKey: "openai-key"}
	rt.cfg.Specialists.Profiles = []SpecialistSubagentProfile{
		{Name: "kernel-investigator", Provider: "openai", Model: "specialist-model", APIKey: "openai-key"},
	}

	if err := rt.activateProvider("openai", "gpt-main-new", ""); err != nil {
		t.Fatalf("activateProvider: %v", err)
	}
	if rt.cfg.ProjectAnalysis.WorkerProfile == nil || rt.cfg.ProjectAnalysis.WorkerProfile.Model != "worker-model" {
		t.Fatalf("expected analysis worker to stay unchanged, got %#v", rt.cfg.ProjectAnalysis.WorkerProfile)
	}
	if rt.cfg.ProjectAnalysis.ReviewerProfile == nil || rt.cfg.ProjectAnalysis.ReviewerProfile.Model != "analysis-reviewer" {
		t.Fatalf("expected analysis reviewer to stay unchanged, got %#v", rt.cfg.ProjectAnalysis.ReviewerProfile)
	}
	profile, ok := configuredSpecialistProfileByName(rt.cfg, "kernel-investigator")
	if !ok || profile.Model != "specialist-model" {
		t.Fatalf("expected specialist model to stay unchanged, got %#v", profile)
	}

	saved, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if saved.ProjectAnalysis.WorkerProfile == nil || saved.ProjectAnalysis.WorkerProfile.Model != "worker-model" {
		t.Fatalf("expected saved analysis worker to stay unchanged, got %#v", saved.ProjectAnalysis.WorkerProfile)
	}
	if saved.ProjectAnalysis.ReviewerProfile == nil || saved.ProjectAnalysis.ReviewerProfile.Model != "analysis-reviewer" {
		t.Fatalf("expected saved analysis reviewer to stay unchanged, got %#v", saved.ProjectAnalysis.ReviewerProfile)
	}
	savedSpecialist, ok := configuredSpecialistProfileByName(saved, "kernel-investigator")
	if !ok || savedSpecialist.Model != "specialist-model" {
		t.Fatalf("expected saved specialist model to stay unchanged, got %#v", savedSpecialist)
	}
}

func TestActivateProviderStoresMainOnlyProfileWithoutCapturingRoleOverrides(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	rt := &runtimeState{
		cfg:    DefaultConfig(workspace),
		ui:     NewUI(),
		writer: &bytes.Buffer{},
		store:  NewSessionStore(filepath.Join(home, "sessions")),
		session: &Session{
			ID:       "session-main-only-profile",
			Provider: "openrouter",
			Model:    "deepseek/deepseek-v4-pro",
			BaseURL:  normalizeOpenRouterBaseURL(""),
		},
		interactive: false,
	}
	rt.cfg.Provider = "openrouter"
	rt.cfg.Model = "deepseek/deepseek-v4-pro"
	rt.cfg.BaseURL = normalizeOpenRouterBaseURL("")
	rt.cfg.ProviderKeys = map[string]string{
		"deepseek":   "deepseek-key",
		"openrouter": "openrouter-key",
	}
	rt.cfg.ProjectAnalysis.WorkerProfile = &Profile{
		Name:     "worker",
		Provider: "openrouter",
		Model:    "deepseek/deepseek-v4-flash",
		BaseURL:  normalizeOpenRouterBaseURL(""),
		APIKey:   "openrouter-key",
	}

	if err := rt.activateProvider("deepseek", "deepseek-v4-pro", normalizeDeepSeekBaseURL("")); err != nil {
		t.Fatalf("activateProvider: %v", err)
	}
	if rt.cfg.ProjectAnalysis.WorkerProfile == nil || rt.cfg.ProjectAnalysis.WorkerProfile.Provider != "openrouter" {
		t.Fatalf("expected explicit analysis worker override to remain active, got %#v", rt.cfg.ProjectAnalysis.WorkerProfile)
	}
	if len(rt.cfg.Profiles) == 0 {
		t.Fatalf("expected activated profile to be saved")
	}
	profile := rt.cfg.Profiles[0]
	if profile.Provider != "deepseek" || profile.Model != "deepseek-v4-pro" {
		t.Fatalf("expected DeepSeek main profile, got %#v", profile)
	}
	if profile.RoleModels != nil {
		t.Fatalf("main provider activation should not capture stale role overrides, got %#v", profile.RoleModels)
	}

	saved, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(saved.Profiles) == 0 || saved.Profiles[0].Provider != "deepseek" {
		t.Fatalf("expected saved DeepSeek profile, got %#v", saved.Profiles)
	}
	if saved.Profiles[0].RoleModels != nil {
		t.Fatalf("expected saved DeepSeek profile to stay main-only, got %#v", saved.Profiles[0].RoleModels)
	}
	if saved.ProjectAnalysis.WorkerProfile == nil || saved.ProjectAnalysis.WorkerProfile.Provider != "openrouter" {
		t.Fatalf("expected explicit saved worker override to remain global, got %#v", saved.ProjectAnalysis.WorkerProfile)
	}
}

func TestCurrentProfileCapturesRoleModelSet(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	rt := &runtimeState{
		cfg:    DefaultConfig(workspace),
		ui:     NewUI(),
		writer: &bytes.Buffer{},
		store:  NewSessionStore(filepath.Join(home, "sessions")),
		session: &Session{
			ID:       "session-profile-role-capture",
			Provider: "openai",
			Model:    "gpt-main",
		},
		interactive: false,
	}
	rt.cfg.Provider = "openai"
	rt.cfg.Model = "gpt-main"
	rt.cfg.ProjectAnalysis.WorkerProfile = &Profile{Name: "worker", Provider: "openrouter", Model: "worker-model"}
	rt.cfg.ProjectAnalysis.ReviewerProfile = &Profile{Name: "reviewer", Provider: "openai", Model: "analysis-reviewer"}
	rt.cfg.Specialists.Profiles = []SpecialistSubagentProfile{
		{Name: "kernel-investigator", Provider: "openai", Model: "specialist-model"},
	}

	rt.rememberCurrentProfile()
	if len(rt.cfg.Profiles) != 1 {
		t.Fatalf("expected one profile, got %#v", rt.cfg.Profiles)
	}
	profile := rt.cfg.Profiles[0]
	if profile.RoleModels == nil {
		t.Fatalf("expected role models to be captured")
	}
	if profile.RoleModels.AnalysisWorker == nil || profile.RoleModels.AnalysisWorker.Model != "worker-model" {
		t.Fatalf("expected analysis worker role model, got %#v", profile.RoleModels.AnalysisWorker)
	}
	if profile.RoleModels.AnalysisReviewer == nil || profile.RoleModels.AnalysisReviewer.Model != "analysis-reviewer" {
		t.Fatalf("expected analysis reviewer role model, got %#v", profile.RoleModels.AnalysisReviewer)
	}
	if len(profile.RoleModels.Specialists) != 1 || profile.RoleModels.Specialists[0].Model != "specialist-model" {
		t.Fatalf("expected specialist role model, got %#v", profile.RoleModels.Specialists)
	}
}

func TestProfileActivationAppliesRoleModelSet(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var output bytes.Buffer
	rt := &runtimeState{
		cfg:    DefaultConfig(workspace),
		ui:     NewUI(),
		writer: &output,
		store:  NewSessionStore(filepath.Join(home, "sessions")),
		session: &Session{
			ID:       "session-profile-role-activate",
			Provider: "openai",
			Model:    "gpt-old",
		},
		interactive: false,
	}
	rt.cfg.Provider = "openai"
	rt.cfg.Model = "gpt-old"
	rt.cfg.ProjectAnalysis.WorkerProfile = &Profile{Name: "old-worker", Provider: "openai", Model: "old-worker"}
	rt.cfg.ProjectAnalysis.ReviewerProfile = &Profile{Name: "old-reviewer", Provider: "openai", Model: "old-analysis-reviewer"}
	rt.cfg.Specialists.Profiles = []SpecialistSubagentProfile{
		{Name: "kernel-investigator", Provider: "openai", Model: "old-specialist", Prompt: "Keep prompt."},
	}
	rt.cfg.Profiles = []Profile{{
		Name:     "full-profile",
		Provider: "openrouter",
		Model:    "main-new",
		RoleModels: &ProfileRoleModels{
			AnalysisWorker:   &Profile{Name: "worker", Provider: "openrouter", Model: "worker-new"},
			AnalysisReviewer: &Profile{Name: "reviewer", Provider: "openai", Model: "analysis-reviewer-new"},
			Specialists: []SpecialistSubagentProfile{
				{Name: "kernel-investigator", Provider: "openai", Model: "specialist-new"},
			},
		},
	}}
	rt.cfg.ProviderKeys = map[string]string{
		"openai":     "openai-key",
		"anthropic":  "anthropic-key",
		"openrouter": "openrouter-key",
	}

	if err := rt.handleProfileCommand("1"); err != nil {
		t.Fatalf("handleProfileCommand: %v", err)
	}
	if rt.cfg.Provider != "openrouter" || rt.cfg.Model != "main-new" {
		t.Fatalf("expected main profile activation, got %s/%s", rt.cfg.Provider, rt.cfg.Model)
	}
	if rt.cfg.ProjectAnalysis.WorkerProfile == nil || rt.cfg.ProjectAnalysis.WorkerProfile.Model != "worker-new" {
		t.Fatalf("expected analysis worker profile to activate, got %#v", rt.cfg.ProjectAnalysis.WorkerProfile)
	}
	if rt.cfg.ProjectAnalysis.ReviewerProfile == nil || rt.cfg.ProjectAnalysis.ReviewerProfile.Model != "analysis-reviewer-new" {
		t.Fatalf("expected analysis reviewer profile to activate, got %#v", rt.cfg.ProjectAnalysis.ReviewerProfile)
	}
	specialist, ok := configuredSpecialistProfileByName(rt.cfg, "kernel-investigator")
	if !ok || specialist.Model != "specialist-new" || specialist.Prompt != "Keep prompt." {
		t.Fatalf("expected specialist model profile to activate while preserving non-model fields, got %#v", specialist)
	}
}

func TestSingleModelProfileActivationClearsStaleAnalysisRoles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var output bytes.Buffer
	rt := &runtimeState{
		cfg:    DefaultConfig(home),
		ui:     NewUI(),
		writer: &output,
		store:  NewSessionStore(filepath.Join(home, "sessions")),
		session: &Session{
			ID:       "session-single-profile",
			Provider: "openai",
			Model:    "gpt-old",
		},
		interactive: false,
	}
	rt.cfg.Provider = "openai"
	rt.cfg.Model = "gpt-old"
	rt.cfg.ProjectAnalysis.WorkerProfile = &Profile{Name: "old-worker", Provider: "openai", Model: "old-worker"}
	rt.cfg.ProjectAnalysis.ReviewerProfile = &Profile{Name: "old-reviewer", Provider: "openai", Model: "old-reviewer"}
	rt.cfg.Profiles = []Profile{{
		Name:     "single-profile",
		Provider: "ollama",
		Model:    "qwen3.5:14b",
	}}

	if err := rt.handleProfileCommand("1"); err != nil {
		t.Fatalf("handleProfileCommand: %v", err)
	}
	if rt.cfg.Provider != "ollama" || rt.cfg.Model != "qwen3.5:14b" {
		t.Fatalf("expected main profile activation, got %s/%s", rt.cfg.Provider, rt.cfg.Model)
	}
	if rt.cfg.ProjectAnalysis.WorkerProfile != nil || rt.cfg.ProjectAnalysis.ReviewerProfile != nil {
		t.Fatalf("expected stale analysis roles to clear, got worker=%#v reviewer=%#v", rt.cfg.ProjectAnalysis.WorkerProfile, rt.cfg.ProjectAnalysis.ReviewerProfile)
	}
	if len(rt.cfg.Profiles) == 0 || rt.cfg.Profiles[0].RoleModels == nil {
		t.Fatalf("expected activated single-model profile to be saved with an empty role model set, got %#v", rt.cfg.Profiles)
	}
}

func TestMainPromptReasoningEffortOnlyForEffortCapableProvider(t *testing.T) {
	rt := &runtimeState{
		cfg: Config{
			Provider:        "openai-codex",
			Model:           "gpt-5.5",
			ReasoningEffort: "high",
		},
		session: &Session{
			Provider: "openai-codex",
			Model:    "gpt-5.5",
		},
	}
	if got := rt.mainPromptReasoningEffort(); got != "high" {
		t.Fatalf("mainPromptReasoningEffort = %q, want high", got)
	}

	rt.session.Provider = "deepseek"
	rt.cfg.Provider = "deepseek"
	rt.cfg.ReasoningEffort = ""
	if got := rt.mainPromptReasoningEffort(); got != "undefined" {
		t.Fatalf("mainPromptReasoningEffort = %q, want undefined", got)
	}

	rt.session.Provider = "openai"
	rt.cfg.Provider = "openai"
	rt.cfg.ReasoningEffort = "high"
	if got := rt.mainPromptReasoningEffort(); got != "" {
		t.Fatalf("mainPromptReasoningEffort = %q, want empty for non-effort provider", got)
	}
}

func TestProfileActivationDefaultsUndefinedReasoningEffortForRoleModel(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var output bytes.Buffer
	rt := &runtimeState{
		cfg:    DefaultConfig(workspace),
		ui:     UI{},
		writer: &output,
		store:  NewSessionStore(filepath.Join(home, "sessions")),
		session: &Session{
			ID:       "session-effort-default-profile-role",
			Provider: "openai",
			Model:    "gpt-old",
		},
		interactive: false,
	}
	rt.cfg.Provider = "openai"
	rt.cfg.Model = "gpt-old"
	rt.cfg.APIKey = "openai-key"
	rt.cfg.ProviderKeys = map[string]string{"openai": "openai-key"}
	rt.cfg.Profiles = []Profile{{
		Name:     "main-with-codex-reviewer",
		Provider: "openai",
		Model:    "gpt-5.4",
		RoleModels: &ProfileRoleModels{
			AnalysisWorker: &Profile{
				Provider: "openai-codex",
				Model:    "gpt-5.5",
			},
		},
	}}

	if err := rt.handleProfileCommand("1"); err != nil {
		t.Fatalf("handleProfileCommand: %v", err)
	}
	if rt.cfg.ReasoningEffort != "" {
		t.Fatalf("expected main reasoning effort to stay unchanged, got %q", rt.cfg.ReasoningEffort)
	}
	if rt.cfg.ProjectAnalysis.WorkerProfile == nil || rt.cfg.ProjectAnalysis.WorkerProfile.ReasoningEffort != "medium" {
		t.Fatalf("expected profile role reasoning effort to default to medium, got %#v", rt.cfg.ProjectAnalysis.WorkerProfile)
	}
	if !strings.Contains(output.String(), "defaulted to medium") {
		t.Fatalf("expected default effort notice, got %q", output.String())
	}
}

func TestEffortCommandSetsMainAndRoleEffortIndependently(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	rt := &runtimeState{
		cfg:    DefaultConfig(workspace),
		ui:     UI{},
		writer: &bytes.Buffer{},
		session: &Session{
			ID:       "session-effort-independent",
			Provider: "openai-codex",
			Model:    "gpt-5.5",
		},
	}
	rt.cfg.Provider = "openai-codex"
	rt.cfg.Model = "gpt-5.5"
	rt.cfg.ReasoningEffort = "low"
	rt.cfg.ProjectAnalysis.WorkerProfile = &Profile{
		Provider:        "openai-codex",
		Model:           "gpt-5.5",
		ReasoningEffort: "medium",
	}

	if err := rt.handleEffortCommand("analysis-worker high"); err != nil {
		t.Fatalf("handleEffortCommand analysis-worker: %v", err)
	}
	if rt.cfg.ReasoningEffort != "low" {
		t.Fatalf("expected main effort to remain low, got %q", rt.cfg.ReasoningEffort)
	}
	if rt.cfg.ProjectAnalysis.WorkerProfile.ReasoningEffort != "high" {
		t.Fatalf("expected analysis-worker effort high, got %q", rt.cfg.ProjectAnalysis.WorkerProfile.ReasoningEffort)
	}

	if err := rt.handleEffortCommand("xhigh"); err != nil {
		t.Fatalf("handleEffortCommand main: %v", err)
	}
	if rt.cfg.ReasoningEffort != "xhigh" {
		t.Fatalf("expected main effort xhigh, got %q", rt.cfg.ReasoningEffort)
	}
	if rt.cfg.ProjectAnalysis.WorkerProfile.ReasoningEffort != "high" {
		t.Fatalf("expected analysis-worker effort to remain high, got %q", rt.cfg.ProjectAnalysis.WorkerProfile.ReasoningEffort)
	}
}

func TestRoleModelStatusDisplaysReasoningEffort(t *testing.T) {
	var output bytes.Buffer
	rt := &runtimeState{
		cfg:    DefaultConfig(t.TempDir()),
		ui:     UI{},
		writer: &output,
	}
	rt.cfg.Provider = "openai-codex"
	rt.cfg.Model = "gpt-5.5"
	rt.cfg.ReasoningEffort = "low"
	rt.cfg.ProjectAnalysis.WorkerProfile = &Profile{
		Provider:        "deepseek",
		Model:           "deepseek-v4-pro",
		ReasoningEffort: "medium",
	}

	if err := rt.showProjectAnalysisModelStatus(); err != nil {
		t.Fatalf("showProjectAnalysisModelStatus: %v", err)
	}
	text := strings.Join(strings.Fields(output.String()), " ")
	for _, want := range []string{
		"worker: DeepSeek / deepseek-v4-pro / effort=medium",
		"reviewer: not configured; follows analysis_worker model -> DeepSeek / deepseek-v4-pro / effort=medium",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected status output to contain %q, got %q", want, text)
		}
	}
}

func TestHandleProfileCommandShowsStoredRoleModelSetInline(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var output bytes.Buffer
	cfg := DefaultConfig(workspace)
	cfg.Profiles = []Profile{{
		Name:     "main",
		Provider: "openai",
		Model:    "gpt-main",
		RoleModels: &ProfileRoleModels{
			AnalysisWorker:   &Profile{Name: "worker", Provider: "ollama", Model: "llama3", BaseURL: "http://localhost:11434"},
			AnalysisReviewer: &Profile{Name: "analysis-reviewer", Provider: "openai", Model: "gpt-analysis-review"},
			Specialists: []SpecialistSubagentProfile{
				{Name: "kernel-investigator", Provider: "openrouter", Model: "meta-llama/llama-3.1-70b"},
			},
		},
	}}
	rt := &runtimeState{
		cfg:    cfg,
		ui:     NewUI(),
		writer: &output,
		session: &Session{
			ID:       "session-profile-role-summary",
			Provider: "openai",
			Model:    "gpt-main",
		},
		interactive: false,
	}

	if err := rt.handleProfileCommand(""); err != nil {
		t.Fatalf("handleProfileCommand: %v", err)
	}
	text := output.String()
	for _, want := range []string{
		"analysis_worker",
		"ollama / llama3",
		"analysis_reviewer",
		"openai-api / gpt-analysis-review",
		"owner:kernel-investigator",
		"openrouter / meta-llama/llama-3.1-70b",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected profile output to contain %q, got %q", want, text)
		}
	}
	if strings.Contains(text, "Role Model Profiles") {
		t.Fatalf("expected profile output to avoid duplicate footer role section, got %q", text)
	}
}

func TestRoleModelActivationReusesStoredProviderKeys(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfg := DefaultConfig(workspace)
	cfg.ProviderKeys = map[string]string{
		"openai":     "openai-key",
		"anthropic":  "anthropic-key",
		"openrouter": "openrouter-key",
	}
	rt := &runtimeState{
		cfg:     cfg,
		ui:      NewUI(),
		writer:  &bytes.Buffer{},
		session: &Session{ID: "session-role-provider-keys"},
	}

	if err := rt.activateProjectAnalysisRole("worker", "anthropic", "claude-worker", "", ""); err != nil {
		t.Fatalf("activateProjectAnalysisRole: %v", err)
	}
	if rt.cfg.ProjectAnalysis.WorkerProfile == nil || rt.cfg.ProjectAnalysis.WorkerProfile.APIKey != "anthropic-key" {
		t.Fatalf("expected analysis worker to reuse provider key, got %#v", rt.cfg.ProjectAnalysis.WorkerProfile)
	}

	if err := rt.activateSpecialistModel("kernel-investigator", "openrouter", "meta-llama/llama-3.1-70b", "", ""); err != nil {
		t.Fatalf("activateSpecialistModel: %v", err)
	}
	profile, ok := configuredSpecialistProfileByName(rt.cfg, "kernel-investigator")
	if !ok || profile.APIKey != "openrouter-key" {
		t.Fatalf("expected specialist to reuse provider key, got %#v", profile)
	}
}

func TestClearSpecialistModelOverrideClearsStoredReasoningEffort(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	rt := &runtimeState{
		cfg:       DefaultConfig(workspace),
		ui:        UI{},
		writer:    &bytes.Buffer{},
		workspace: Workspace{BaseRoot: workspace, Root: workspace},
	}
	rt.cfg.Specialists.Profiles = []SpecialistSubagentProfile{{
		Name:            "kernel-investigator",
		Prompt:          "Keep this non-model override.",
		Provider:        "openai-codex",
		Model:           "gpt-5.5",
		ReasoningEffort: "high",
	}}

	if err := rt.clearSpecialistModelOverride("kernel-investigator"); err != nil {
		t.Fatalf("clearSpecialistModelOverride: %v", err)
	}
	profile, ok := configuredSpecialistProfileByName(rt.cfg, "kernel-investigator")
	if !ok {
		t.Fatalf("expected non-model specialist override to remain")
	}
	if profile.Provider != "" || profile.Model != "" || profile.ReasoningEffort != "" {
		t.Fatalf("expected model and effort override to clear, got %#v", profile)
	}
	if profile.Prompt != "Keep this non-model override." {
		t.Fatalf("expected prompt override to remain, got %#v", profile)
	}
}

func TestRuntimeStatePromptResolveAutoVerifyFailureDisableSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	output := &bytes.Buffer{}
	rt := &runtimeState{
		reader:      bufio.NewReader(strings.NewReader("y\n")),
		writer:      output,
		ui:          UI{},
		interactive: true,
		detectVerificationToolPath: func(string) string {
			return ""
		},
		cfg: DefaultConfig(home),
		session: &Session{
			Provider:       "openai",
			Model:          "gpt-test",
			BaseURL:        "https://example.test",
			PermissionMode: "default",
		},
	}
	rt.agent = &Agent{
		Config: rt.cfg,
	}

	resolution, err := rt.promptResolveAutoVerifyFailure(VerificationReport{
		Steps: []VerificationStep{{
			Label:       "msbuild demo.sln",
			Command:     "msbuild demo.sln /m",
			Status:      VerificationFailed,
			FailureKind: "command_not_found",
			Hint:        "A required verification tool could not be started.",
		}},
	})
	if err != nil {
		t.Fatalf("promptResolveAutoVerifyFailure: %v", err)
	}
	if resolution != AutoVerifyFailureDisable {
		t.Fatalf("expected disable resolution, got %q", resolution)
	}
	if configAutoVerify(rt.cfg) {
		t.Fatalf("expected runtime config auto_verify to be disabled")
	}
	if configAutoVerify(rt.agent.Config) {
		t.Fatalf("expected agent config auto_verify to be disabled")
	}
}

func TestRuntimeStatePromptResolveAutoVerifyFailureAlwaysPersistsToWorkspaceConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	workspace := filepath.Join(home, "repo")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	cfgForTrust := DefaultConfig(workspace)
	markConfigProjectTrustedForTest(t, &cfgForTrust, workspace)
	if err := SaveUserConfig(cfgForTrust); err != nil {
		t.Fatalf("SaveUserConfig: %v", err)
	}

	output := &bytes.Buffer{}
	rt := &runtimeState{
		reader:      bufio.NewReader(strings.NewReader("a\n")),
		writer:      output,
		ui:          UI{},
		interactive: true,
		detectVerificationToolPath: func(string) string {
			return ""
		},
		cfg: DefaultConfig(workspace),
		workspace: Workspace{
			BaseRoot: workspace,
			Root:     workspace,
		},
		session: &Session{
			Provider:       "openai",
			Model:          "gpt-test",
			BaseURL:        "https://example.test",
			PermissionMode: "default",
		},
	}
	rt.agent = &Agent{
		Config: rt.cfg,
	}

	resolution, err := rt.promptResolveAutoVerifyFailure(VerificationReport{
		Steps: []VerificationStep{{
			Label:       "msbuild demo.sln",
			Command:     "msbuild demo.sln /m",
			Status:      VerificationFailed,
			FailureKind: "command_not_found",
			Hint:        "A required verification tool could not be started.",
		}},
	})
	if err != nil {
		t.Fatalf("promptResolveAutoVerifyFailure: %v", err)
	}
	if resolution != AutoVerifyFailureDisable {
		t.Fatalf("expected disable resolution, got %q", resolution)
	}

	loaded, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if configAutoVerify(loaded) {
		t.Fatalf("expected workspace config to keep auto_verify disabled")
	}
}

func TestRuntimeStatePromptResolveAutoVerifyFailureSavesToolPathAndRequestsRetry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	workspace := filepath.Join(home, "repo")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	cfgForTrust := DefaultConfig(workspace)
	markConfigProjectTrustedForTest(t, &cfgForTrust, workspace)
	if err := SaveUserConfig(cfgForTrust); err != nil {
		t.Fatalf("SaveUserConfig: %v", err)
	}
	msbuildPath := filepath.Join(home, "MSBuild.exe")
	if err := os.WriteFile(msbuildPath, []byte("stub"), 0o644); err != nil {
		t.Fatalf("write msbuild stub: %v", err)
	}

	output := &bytes.Buffer{}
	rt := &runtimeState{
		reader:      bufio.NewReader(strings.NewReader("p\n" + msbuildPath + "\n")),
		writer:      output,
		ui:          UI{},
		interactive: true,
		detectVerificationToolPath: func(string) string {
			return ""
		},
		cfg: DefaultConfig(workspace),
		workspace: Workspace{
			BaseRoot: workspace,
			Root:     workspace,
		},
		session: &Session{
			Provider:       "openai",
			Model:          "gpt-test",
			BaseURL:        "https://example.test",
			PermissionMode: "default",
		},
	}
	rt.agent = &Agent{
		Config: rt.cfg,
	}

	resolution, err := rt.promptResolveAutoVerifyFailure(VerificationReport{
		Steps: []VerificationStep{{
			Label:       "msbuild demo.sln",
			Command:     "msbuild demo.sln /m",
			Status:      VerificationFailed,
			FailureKind: "command_not_found",
			Hint:        "A required verification tool could not be started.",
		}},
	})
	if err != nil {
		t.Fatalf("promptResolveAutoVerifyFailure: %v", err)
	}
	if resolution != AutoVerifyFailureRetry {
		t.Fatalf("expected retry resolution, got %q", resolution)
	}
	if strings.TrimSpace(rt.cfg.MSBuildPath) != msbuildPath {
		t.Fatalf("expected runtime config msbuild path to be updated, got %q", rt.cfg.MSBuildPath)
	}
	if got := strings.TrimSpace(rt.workspace.VerificationToolPaths["msbuild"]); got != msbuildPath {
		t.Fatalf("expected workspace tool path to be updated, got %q", got)
	}

	loaded, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if strings.TrimSpace(loaded.MSBuildPath) != msbuildPath {
		t.Fatalf("expected workspace config msbuild path to persist, got %q", loaded.MSBuildPath)
	}
}

func TestRuntimeStatePromptResolveAutoVerifyFailureAutoDetectsAndRetries(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only auto detection behavior")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	workspace := filepath.Join(home, "repo")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	output := &bytes.Buffer{}
	rt := &runtimeState{
		reader:      bufio.NewReader(strings.NewReader("")),
		writer:      output,
		ui:          UI{},
		interactive: true,
		detectVerificationToolPath: func(tool string) string {
			if tool == "msbuild" {
				return `C:\Tools\MSBuild\MSBuild.exe`
			}
			return ""
		},
		cfg: DefaultConfig(workspace),
		workspace: Workspace{
			BaseRoot: workspace,
			Root:     workspace,
		},
		session: &Session{
			Provider:       "openai",
			Model:          "gpt-test",
			BaseURL:        "https://example.test",
			PermissionMode: "default",
		},
	}
	rt.agent = &Agent{
		Config: rt.cfg,
	}

	resolution, err := rt.promptResolveAutoVerifyFailure(VerificationReport{
		Steps: []VerificationStep{{
			Label:       "msbuild demo.sln",
			Command:     "msbuild demo.sln /m",
			Status:      VerificationFailed,
			FailureKind: "command_not_found",
			Hint:        "A required verification tool could not be started.",
		}},
	})
	if err != nil {
		t.Fatalf("promptResolveAutoVerifyFailure: %v", err)
	}
	if resolution != AutoVerifyFailureRetry {
		t.Fatalf("expected retry resolution, got %q", resolution)
	}
	if strings.TrimSpace(rt.cfg.MSBuildPath) != `C:\Tools\MSBuild\MSBuild.exe` {
		t.Fatalf("expected runtime config msbuild path to update, got %q", rt.cfg.MSBuildPath)
	}
	if got := strings.TrimSpace(rt.workspace.VerificationToolPaths["msbuild"]); got != `C:\Tools\MSBuild\MSBuild.exe` {
		t.Fatalf("expected workspace verification tool path to update, got %q", got)
	}
	if !strings.Contains(output.String(), "Retrying automatic verification") {
		t.Fatalf("expected retry notice in output, got %q", output.String())
	}
}

func TestBuildVerificationToolPathsIncludesCTestAndNinja(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.CTestPath = `C:\Tools\CMake\bin\ctest.exe`
	cfg.NinjaPath = `C:\Tools\Ninja\ninja.exe`

	paths := buildVerificationToolPaths(cfg)
	if got := paths["ctest"]; got != cfg.CTestPath {
		t.Fatalf("expected ctest path, got %q", got)
	}
	if got := paths["ninja"]; got != cfg.NinjaPath {
		t.Fatalf("expected ninja path, got %q", got)
	}
}

func TestHandleSetVerificationToolPathCommandPersistsWorkspaceOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	workspace := filepath.Join(home, "repo")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	cfgForTrust := DefaultConfig(workspace)
	markConfigProjectTrustedForTest(t, &cfgForTrust, workspace)
	if err := SaveUserConfig(cfgForTrust); err != nil {
		t.Fatalf("SaveUserConfig: %v", err)
	}
	ninjaPath := filepath.Join(home, "ninja.exe")
	if err := os.WriteFile(ninjaPath, []byte("stub"), 0o644); err != nil {
		t.Fatalf("write ninja stub: %v", err)
	}

	output := &bytes.Buffer{}
	rt := &runtimeState{
		writer: output,
		ui:     UI{},
		cfg:    DefaultConfig(workspace),
		workspace: Workspace{
			BaseRoot: workspace,
			Root:     workspace,
		},
		session: &Session{
			Provider:       "openai",
			Model:          "gpt-test",
			BaseURL:        "https://example.test",
			PermissionMode: "default",
		},
	}
	rt.agent = &Agent{
		Config: rt.cfg,
	}

	if err := rt.handleSetVerificationToolPathCommand("ninja", ninjaPath); err != nil {
		t.Fatalf("handleSetVerificationToolPathCommand: %v", err)
	}
	if strings.TrimSpace(rt.cfg.NinjaPath) != ninjaPath {
		t.Fatalf("expected runtime ninja path to update, got %q", rt.cfg.NinjaPath)
	}
	if strings.TrimSpace(rt.workspace.VerificationToolPaths["ninja"]) != ninjaPath {
		t.Fatalf("expected workspace ninja path to update")
	}

	loaded, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if strings.TrimSpace(loaded.NinjaPath) != ninjaPath {
		t.Fatalf("expected persisted ninja path, got %q", loaded.NinjaPath)
	}
}

func TestAutoPopulateVerificationToolPathsAppliesDetectedValues(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	detected, err := autoPopulateVerificationToolPaths(t.TempDir(), &cfg, func(tool string) string {
		switch tool {
		case "msbuild":
			return `C:\Tools\MSBuild\MSBuild.exe`
		case "ctest":
			return `C:\Tools\CMake\bin\ctest.exe`
		default:
			return ""
		}
	})
	if runtime.GOOS != "windows" {
		if err != nil {
			t.Fatalf("expected no error on non-windows, got %v", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("autoPopulateVerificationToolPaths: %v", err)
	}
	if detected["msbuild"] == "" || detected["ctest"] == "" {
		t.Fatalf("expected detected paths, got %#v", detected)
	}
	if cfg.MSBuildPath == "" || cfg.CTestPath == "" {
		t.Fatalf("expected config to be updated, got %#v", cfg)
	}
}

func TestHandleClearVerificationToolPathCommandRemovesWorkspaceOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	workspace := filepath.Join(home, "repo")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := SaveWorkspaceConfigOverrides(workspace, map[string]any{
		"cmake_path": `C:\Tools\CMake\bin\cmake.exe`,
	}); err != nil {
		t.Fatalf("SaveWorkspaceConfigOverrides: %v", err)
	}

	rt := &runtimeState{
		writer: os.Stdout,
		ui:     UI{},
		cfg:    DefaultConfig(workspace),
		workspace: Workspace{
			BaseRoot: workspace,
			Root:     workspace,
		},
		session: &Session{
			Provider:       "openai",
			Model:          "gpt-test",
			BaseURL:        "https://example.test",
			PermissionMode: "default",
		},
	}
	rt.cfg.CMakePath = `C:\Tools\CMake\bin\cmake.exe`
	rt.workspace.VerificationToolPaths = buildVerificationToolPaths(rt.cfg)

	if err := rt.handleClearVerificationToolPathCommand("cmake"); err != nil {
		t.Fatalf("handleClearVerificationToolPathCommand: %v", err)
	}
	if strings.TrimSpace(rt.cfg.CMakePath) != "" {
		t.Fatalf("expected runtime cmake path to be cleared")
	}
	if _, ok := rt.workspace.VerificationToolPaths["cmake"]; ok {
		t.Fatalf("expected workspace tool path to be cleared")
	}

	loaded, err := LoadConfig(workspace)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if strings.TrimSpace(loaded.CMakePath) != "" {
		t.Fatalf("expected persisted cmake path to be cleared, got %q", loaded.CMakePath)
	}
}

func TestHandleDetectVerificationToolsCommandUpdatesWorkspaceConfig(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only auto detection behavior")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	workspace := filepath.Join(home, "repo")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	cfg := DefaultConfig(workspace)
	applied, err := autoPopulateVerificationToolPaths(workspace, &cfg, func(tool string) string {
		switch tool {
		case "ninja":
			return `C:\Tools\Ninja\ninja.exe`
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("autoPopulateVerificationToolPaths: %v", err)
	}
	if strings.TrimSpace(applied["ninja"]) == "" {
		t.Fatalf("expected ninja detection result")
	}
}

func TestParseConfirmationAnswerRecognizesAlways(t *testing.T) {
	allowed, always, handled := parseConfirmationAnswer("always")
	if !handled || !allowed || !always {
		t.Fatalf("expected always answer to enable approval, got handled=%v allowed=%v always=%v", handled, allowed, always)
	}
}

func TestRuntimeStateConfirmAlwaysApprovesFutureWritePrompts(t *testing.T) {
	rt := &runtimeState{
		reader:      bufio.NewReader(strings.NewReader("always\n")),
		writer:      &bytes.Buffer{},
		ui:          UI{},
		interactive: true,
	}

	allowed, err := rt.confirm("Allow write? C:\\git\\kernforge\\main.go (add 'always' to allow for entire session)")
	if err != nil {
		t.Fatalf("confirm returned error: %v", err)
	}
	if !allowed {
		t.Fatalf("expected first write prompt to be allowed")
	}
	if !rt.alwaysApproveWrites {
		t.Fatalf("expected write prompts to be auto-approved for the rest of the session")
	}
	if !rt.autoApproveConfirmation("Allow write? C:\\git\\kernforge\\other.go (add 'always' to allow for entire session)") {
		t.Fatalf("expected subsequent write prompt to be auto-approved")
	}
}

func TestRuntimeStateConfirmAlwaysApprovesFutureDiffPreviewPrompts(t *testing.T) {
	rt := &runtimeState{
		reader:      bufio.NewReader(strings.NewReader("a\n")),
		writer:      &bytes.Buffer{},
		ui:          UI{},
		cfg:         Config{AutoLocale: boolPtr(false)},
		interactive: true,
	}

	allowed, err := rt.confirm("Open diff preview?")
	if err != nil {
		t.Fatalf("confirm returned error: %v", err)
	}
	if !allowed {
		t.Fatalf("expected diff preview prompt to be allowed")
	}
	if !rt.alwaysApprovePreview {
		t.Fatalf("expected diff preview prompts to be auto-approved for the rest of the session")
	}
	if !rt.autoAcceptPreviewOnce {
		t.Fatalf("expected first diff preview always answer to auto-accept the current edit")
	}
	if !rt.autoApproveConfirmation("Open diff preview?") {
		t.Fatalf("expected subsequent diff preview prompt to be auto-approved")
	}
}

func TestRuntimeStatePromptContinueReviewRepairUsesPinnedYNPrompt(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		reader:      bufio.NewReader(strings.NewReader("y\n")),
		writer:      &out,
		ui:          UI{},
		cfg:         Config{AutoLocale: boolPtr(false)},
		interactive: true,
	}

	allowed, err := rt.promptContinueReviewRepair("review summary")
	if err != nil {
		t.Fatalf("promptContinueReviewRepair returned error: %v", err)
	}
	if !allowed {
		t.Fatalf("expected y to continue repair")
	}
	text := out.String()
	if !strings.Contains(text, "review summary") {
		t.Fatalf("expected prompt body to be printed, got %q", text)
	}
	if !strings.Contains(text, "Continue repairing?") || !strings.Contains(text, "[y/N, Esc=cancel]") {
		t.Fatalf("expected y/N runtime prompt, got %q", text)
	}
	if strings.Contains(text, "a=auto-accept") {
		t.Fatalf("review repair prompt must not offer auto-accept, got %q", text)
	}
}

func TestRuntimeStateConfirmAlwaysApprovesFutureGitPrompts(t *testing.T) {
	rt := &runtimeState{
		reader:      bufio.NewReader(strings.NewReader("a\n")),
		writer:      &bytes.Buffer{},
		ui:          UI{},
		interactive: true,
		perms:       NewPermissionManager(ModeDefault, nil),
	}

	allowed, err := rt.confirm("Allow git? create commit: test subject (add 'always' to allow for entire session)")
	if err != nil {
		t.Fatalf("confirm returned error: %v", err)
	}
	if !allowed {
		t.Fatalf("expected git prompt to be allowed")
	}
	if rt.perms == nil || !rt.perms.IsGitAllowed() {
		t.Fatalf("expected git prompts to be auto-approved for the rest of the session")
	}
	if !rt.autoApproveConfirmation("Allow git? stage changes with git_add (add 'always' to allow for entire session)") {
		t.Fatalf("expected subsequent git prompt to be auto-approved")
	}
}

func TestStatusCommandFocusesOnRuntimeState(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "https://example.test", "default")
	var out bytes.Buffer
	rt := &runtimeState{
		writer:    &out,
		ui:        UI{},
		cfg:       DefaultConfig(root),
		session:   session,
		store:     store,
		perms:     NewPermissionManager(ModeBypass, nil),
		workspace: Workspace{BaseRoot: root, Root: root},
	}
	rt.alwaysApproveWrites = true

	if _, err := rt.handleCommand(Command{Name: "status"}); err != nil {
		t.Fatalf("handleCommand(status): %v", err)
	}

	text := out.String()
	if !strings.Contains(text, "Current session and runtime state.") {
		t.Fatalf("expected runtime hint, got %q", text)
	}
	if !strings.Contains(text, "write_approval:") {
		t.Fatalf("expected runtime approval state in status, got %q", text)
	}
	if !strings.Contains(text, "permission_mode:          bypassPermissions") || !strings.Contains(text, "active_permission_profile -> :danger-full-access") {
		t.Fatalf("expected live permission profile snapshot in status, got %q", text)
	}
	if !strings.Contains(text, "-- Connection ") || !strings.Contains(text, "-- Approvals ") || !strings.Contains(text, "-- Extensions ") {
		t.Fatalf("expected grouped status output, got %q", text)
	}
	if strings.Contains(text, "auto_checkpoint_edits:") {
		t.Fatalf("did not expect config-only auto_checkpoint_edits in status, got %q", text)
	}
	if strings.Contains(text, "hooks_enabled:") {
		t.Fatalf("did not expect config-only hooks_enabled in status, got %q", text)
	}
}

func TestStatusCommandShowsEditLoopState(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "https://example.test", "default")
	session.RecordEditLoopEvent("fix main", EditLoopEvent{
		Kind:         "apply",
		Source:       "main",
		ToolName:     "write_file",
		Summary:      "wrote main.go",
		Status:       "ok",
		ChangedPaths: []string{"main.go"},
	})
	session.RecordEditLoopEvent("fix main", EditLoopEvent{
		Kind:     "verification",
		Source:   "auto",
		ToolName: "verify",
		Summary:  "go test ./... passed",
		Status:   "passed",
	})
	var out bytes.Buffer
	rt := &runtimeState{
		writer:    &out,
		ui:        UI{},
		cfg:       DefaultConfig(root),
		session:   session,
		store:     store,
		perms:     NewPermissionManager(ModeDefault, nil),
		workspace: Workspace{BaseRoot: root, Root: root},
	}

	if _, err := rt.handleCommand(Command{Name: "status"}); err != nil {
		t.Fatalf("handleCommand(status): %v", err)
	}

	text := out.String()
	for _, want := range []string{"-- Edit Loop ", "edit_loop_status:", "worker_evidence:", "verification_evidence:", "main.go"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected status output to include %q, got %q", want, text)
		}
	}
}

func TestConfigCommandFocusesOnEffectiveSettings(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "https://example.test", "default")
	var out bytes.Buffer
	rt := &runtimeState{
		writer:    &out,
		ui:        UI{},
		cfg:       DefaultConfig(root),
		session:   session,
		store:     store,
		perms:     NewPermissionManager(ModeDefault, nil),
		workspace: Workspace{BaseRoot: root, Root: root},
	}
	rt.alwaysApproveWrites = true

	if _, err := rt.handleCommand(Command{Name: "config"}); err != nil {
		t.Fatalf("handleCommand(config): %v", err)
	}

	text := out.String()
	if !strings.Contains(text, "Effective settings merged from user config, trusted project config, environment, and session overrides.") {
		t.Fatalf("expected config hint, got %q", text)
	}
	if !strings.Contains(text, "auto_checkpoint_edits:") {
		t.Fatalf("expected config settings in config output, got %q", text)
	}
	if !strings.Contains(text, "permission_mode:          default") || !strings.Contains(text, "active_permission_profile -> :workspace") {
		t.Fatalf("expected effective permission profile in config output, got %q", text)
	}
	if !strings.Contains(text, "hooks_enabled:") {
		t.Fatalf("expected hook settings in config output, got %q", text)
	}
	if !strings.Contains(text, "-- Project Trust ") || !strings.Contains(text, "project_config:") {
		t.Fatalf("expected project trust settings in config output, got %q", text)
	}
	if !strings.Contains(text, "-- Model ") || !strings.Contains(text, "-- Tool Paths ") || !strings.Contains(text, "-- Extensions ") {
		t.Fatalf("expected grouped config output, got %q", text)
	}
	if strings.Contains(text, "write_approval:") {
		t.Fatalf("did not expect runtime-only approval state in config, got %q", text)
	}
}

func TestSlashCommandTurnElapsedPolicySuppressesLocalMetaCommands(t *testing.T) {
	cases := []string{
		"/exit",
		"/quit",
		"/status",
		"/config",
		"/trust",
		"/model gpt-5.5",
		"/provider openrouter",
		"/profile",
		"/help",
		"/version",
		"/clear",
		"/reload",
		"/selection",
		"/mem recent",
	}
	for _, input := range cases {
		cmd, ok := ParseCommand(input)
		if !ok {
			t.Fatalf("expected %q to parse as slash command", input)
		}
		if slashCommandShouldPrintTurnElapsed(cmd) {
			t.Fatalf("expected %q to suppress turn elapsed", input)
		}
	}
}

func TestSlashCommandTurnElapsedPolicyKeepsWorkCommands(t *testing.T) {
	cases := []string{
		"/review",
		"/verify",
		"/analyze-project",
	}
	for _, input := range cases {
		cmd, ok := ParseCommand(input)
		if !ok {
			t.Fatalf("expected %q to parse as slash command", input)
		}
		if !slashCommandShouldPrintTurnElapsed(cmd) {
			t.Fatalf("expected %q to print turn elapsed", input)
		}
	}
}

func TestProgressDisplayCommandShowsAndSetsMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := t.TempDir()
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
		cfg:    DefaultConfig(root),
	}

	if _, err := rt.handleCommand(Command{Name: "progress-display"}); err != nil {
		t.Fatalf("handleCommand(progress-display): %v", err)
	}
	if !strings.Contains(out.String(), "progress_display: compact") {
		t.Fatalf("expected current progress display, got %q", out.String())
	}

	out.Reset()
	if _, err := rt.handleCommand(Command{Name: "progress_display", Args: "stream"}); err != nil {
		t.Fatalf("handleCommand(progress-display stream): %v", err)
	}
	if got := configProgressDisplay(rt.cfg); got != "stream" {
		t.Fatalf("expected runtime progress_display stream, got %q", got)
	}
	loaded, err := LoadConfig(root)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := configProgressDisplay(loaded); got != "stream" {
		t.Fatalf("expected saved progress_display stream, got %q", got)
	}
	if !strings.Contains(out.String(), "progress_display set to stream") {
		t.Fatalf("expected success output, got %q", out.String())
	}

	if _, err := rt.handleCommand(Command{Name: "progress-display", Args: "surprise"}); err == nil {
		t.Fatalf("expected invalid progress display to fail")
	}
}

func TestConfigCommandShowsWebResearchMCPKeySources(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BRAVE_SEARCH_API_KEY", "env-secret")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "https://example.test", "default")
	cfg := DefaultConfig(root)
	cfg.MCPServers = []MCPServerConfig{
		{
			Name:    "web-research",
			Command: "node",
			Args:    []string{".kernforge/mcp/web-research-mcp.js"},
			Env: map[string]string{
				"TAVILY_API_KEY":       "config-secret",
				"BRAVE_SEARCH_API_KEY": "",
				"SERPAPI_API_KEY":      "",
			},
			Capabilities: []string{"web_search", "web_fetch"},
		},
	}
	var out bytes.Buffer
	rt := &runtimeState{
		writer:    &out,
		ui:        UI{},
		cfg:       cfg,
		session:   session,
		store:     store,
		perms:     NewPermissionManager(ModeDefault, nil),
		workspace: Workspace{BaseRoot: root, Root: root},
	}

	if _, err := rt.handleCommand(Command{Name: "config"}); err != nil {
		t.Fatalf("handleCommand(config): %v", err)
	}

	text := out.String()
	normalized := strings.Join(strings.Fields(text), " ")
	for _, needle := range []string{
		"-- Web Research MCP ",
		"configured: true",
		"servers: web-research",
		"active_search_key: TAVILY_API_KEY (config)",
		"tavily_api_key: config",
		"brave_search_api_key: environment",
		"serpapi_api_key: unset",
	} {
		if !strings.Contains(normalized, needle) {
			t.Fatalf("expected normalized config output to contain %q, got %q", needle, normalized)
		}
	}
}

func TestMCPCommandShowsWebResearchMCPKeySources(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BRAVE_SEARCH_API_KEY", "env-secret")
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "https://example.test", "default")
	cfg := DefaultConfig(root)
	cfg.MCPServers = []MCPServerConfig{
		{
			Name:    "web-research",
			Command: "node",
			Args:    []string{".kernforge/mcp/web-research-mcp.js"},
			Env: map[string]string{
				"TAVILY_API_KEY":       "config-secret",
				"BRAVE_SEARCH_API_KEY": "",
				"SERPAPI_API_KEY":      "",
			},
			Capabilities: []string{"web_search", "web_fetch"},
		},
	}
	var out bytes.Buffer
	rt := &runtimeState{
		writer:    &out,
		ui:        UI{},
		cfg:       cfg,
		session:   session,
		store:     store,
		perms:     NewPermissionManager(ModeDefault, nil),
		workspace: Workspace{BaseRoot: root, Root: root},
		mcp: &MCPManager{
			servers: []*MCPClient{
				{
					config: cfg.MCPServers[0],
					status: MCPServerStatus{
						Name:          "web-research",
						Command:       "node",
						Cwd:           root,
						ToolCount:     2,
						ResourceCount: 0,
						PromptCount:   0,
					},
				},
			},
			status: []MCPServerStatus{
				{
					Name:          "web-research",
					Command:       "node",
					Cwd:           root,
					ToolCount:     2,
					ResourceCount: 0,
					PromptCount:   0,
				},
			},
		},
	}

	if _, err := rt.handleCommand(Command{Name: "mcp"}); err != nil {
		t.Fatalf("handleCommand(mcp): %v", err)
	}

	normalized := strings.Join(strings.Fields(out.String()), " ")
	for _, needle := range []string{
		"== MCP",
		"web-research tools=2 resources=0 prompts=0",
		"env=local",
		"active_key=TAVILY_API_KEY (config)",
		"tavily=config",
		"brave=environment",
		"serpapi=unset",
	} {
		if !strings.Contains(normalized, needle) {
			t.Fatalf("expected normalized mcp output to contain %q, got %q", needle, normalized)
		}
	}
}

func TestTasksCommandRendersSummaryAndModernPlanItems(t *testing.T) {
	root := t.TempDir()
	store := NewSessionStore(filepath.Join(root, "sessions"))
	session := NewSession(root, "openrouter", "google/gemini-2.5-pro", "https://example.test", "default")
	session.Plan = []PlanItem{
		{Step: "Inspect UI render path", Status: "completed"},
		{Step: "Refine status layout", Status: "in_progress"},
		{Step: "Polish task cards", Status: "pending"},
	}
	var out bytes.Buffer
	rt := &runtimeState{
		writer:    &out,
		ui:        UI{},
		cfg:       DefaultConfig(root),
		session:   session,
		store:     store,
		perms:     NewPermissionManager(ModeDefault, nil),
		workspace: Workspace{BaseRoot: root, Root: root},
	}

	if _, err := rt.handleCommand(Command{Name: "tasks"}); err != nil {
		t.Fatalf("handleCommand(tasks): %v", err)
	}

	text := out.String()
	for _, needle := range []string{
		"-- Summary ",
		"-- Plan ",
		"completed:",
		"in_progress:",
		"pending:",
		"01. [done] Inspect UI render path",
		"02. [work] Refine status layout",
		"03. [todo] Polish task cards",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("expected tasks output to contain %q, got %q", needle, text)
		}
	}
}

func TestRuntimeStatePreviewEditSkipsPreviewWhenAlwaysEnabled(t *testing.T) {
	rt := &runtimeState{
		writer:               &bytes.Buffer{},
		ui:                   UI{},
		interactive:          true,
		alwaysApprovePreview: true,
	}

	ok, err := rt.previewEdit(EditPreview{
		Title:   "Apply patch",
		Preview: "Preview for main.go\n+ change",
	})
	if err != nil {
		t.Fatalf("previewEdit returned error: %v", err)
	}
	if !ok {
		t.Fatalf("expected previewEdit to auto-accept without opening diff preview")
	}
}

func TestRuntimeStatePreviewEditAutoAcceptsCurrentDiffPreviewAlwaysAnswer(t *testing.T) {
	rt := &runtimeState{
		reader:      bufio.NewReader(strings.NewReader("a\n")),
		writer:      &bytes.Buffer{},
		ui:          UI{},
		cfg:         Config{AutoLocale: boolPtr(false)},
		interactive: true,
	}

	ok, err := rt.previewEdit(EditPreview{
		Title:   "Apply patch",
		Preview: "Preview for main.go\n+ change",
	})
	if err != nil {
		t.Fatalf("previewEdit returned error: %v", err)
	}
	if !ok {
		t.Fatalf("expected previewEdit to auto-accept after 'a' response")
	}
	if rt.autoAcceptPreviewOnce {
		t.Fatalf("expected one-shot auto-accept flag to be consumed")
	}
}

func TestRuntimeStatePreviewEditConsumesPreArmedAutoAcceptOnceWithoutPrompt(t *testing.T) {
	rt := &runtimeState{
		reader:                bufio.NewReader(strings.NewReader("")),
		writer:                &bytes.Buffer{},
		ui:                    UI{},
		cfg:                   Config{AutoLocale: boolPtr(false)},
		interactive:           false,
		autoAcceptPreviewOnce: true,
	}

	ok, err := rt.previewEdit(EditPreview{
		Title:   "Apply patch",
		Preview: "Preview for main.go\n+ change",
	})
	if err != nil {
		t.Fatalf("previewEdit returned error: %v", err)
	}
	if !ok {
		t.Fatalf("expected pre-armed one-shot preview approval to allow the edit")
	}
	if rt.autoAcceptPreviewOnce {
		t.Fatalf("expected one-shot auto-accept flag to be consumed")
	}
}

func TestRuntimeStatePreviewEditNonInteractiveReadsPipedAnswer(t *testing.T) {
	rt := &runtimeState{
		reader:      bufio.NewReader(strings.NewReader("a\n")),
		writer:      &bytes.Buffer{},
		ui:          UI{},
		cfg:         Config{AutoLocale: boolPtr(false)},
		interactive: false,
	}

	ok, err := rt.previewEdit(EditPreview{
		Title:   "Apply patch",
		Preview: "Preview for main.go\n+ change",
	})
	if err != nil {
		t.Fatalf("previewEdit returned error: %v", err)
	}
	if !ok {
		t.Fatalf("expected piped diff preview answer to allow the edit")
	}
}

func TestRuntimeStatePreviewEditNonInteractiveEOFDoesNotAutoAccept(t *testing.T) {
	rt := &runtimeState{
		reader:      bufio.NewReader(strings.NewReader("")),
		writer:      &bytes.Buffer{},
		ui:          UI{},
		cfg:         Config{AutoLocale: boolPtr(false)},
		interactive: false,
	}

	ok, err := rt.previewEdit(EditPreview{
		Title:   "Apply patch",
		Preview: "Preview for main.go\n+ change",
	})
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF instead of implicit diff preview approval, ok=%v err=%v", ok, err)
	}
}

func TestRuntimeStatePromptConfirmAutoVerifyNonInteractiveBypassApproves(t *testing.T) {
	rt := &runtimeState{
		writer:      &bytes.Buffer{},
		ui:          UI{},
		interactive: false,
		perms:       NewPermissionManager(ModeBypass, nil),
	}

	ok, err := rt.promptConfirmAutoVerify(VerificationPlan{
		Mode: VerificationAdaptive,
		Steps: []VerificationStep{{
			Label:   "focused verification",
			Command: "go test ./...",
			Status:  VerificationPending,
		}},
	})
	if err != nil {
		t.Fatalf("promptConfirmAutoVerify returned error: %v", err)
	}
	if !ok {
		t.Fatalf("noninteractive bypass mode should auto-approve verification")
	}
}

func TestRuntimeStatePromptConfirmAutoVerifyNonInteractiveDefaultDeclines(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer:      &out,
		ui:          UI{},
		cfg:         Config{AutoLocale: boolPtr(false)},
		interactive: false,
		perms:       NewPermissionManager(ModeDefault, nil),
	}

	ok, err := rt.promptConfirmAutoVerify(VerificationPlan{
		Mode: VerificationAdaptive,
		Steps: []VerificationStep{{
			Label:   "focused verification",
			Command: "go test ./...",
			Status:  VerificationPending,
		}},
	})
	if err != nil {
		t.Fatalf("promptConfirmAutoVerify returned error: %v", err)
	}
	if ok {
		t.Fatalf("noninteractive default mode should not run verification without approval")
	}
	output := out.String()
	for _, want := range []string{"Automatic verification plan:", "focused verification", "Run automatic verification now?", "a=auto-run"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected noninteractive verification prompt output to contain %q, got %q", want, output)
		}
	}
}

func TestRuntimeStatePromptConfirmAutoVerifyNonInteractiveReadsPipedAnswer(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		reader:      bufio.NewReader(strings.NewReader("y\n")),
		writer:      &out,
		ui:          UI{},
		cfg:         Config{AutoLocale: boolPtr(false)},
		interactive: false,
		perms:       NewPermissionManager(ModeDefault, nil),
	}

	ok, err := rt.promptConfirmAutoVerify(VerificationPlan{
		Mode: VerificationAdaptive,
		Steps: []VerificationStep{{
			Label:   "focused verification",
			Command: "go test ./...",
			Status:  VerificationPending,
		}},
	})
	if err != nil {
		t.Fatalf("promptConfirmAutoVerify returned error: %v", err)
	}
	if !ok {
		t.Fatalf("expected piped verification answer to allow verification")
	}
	output := out.String()
	for _, want := range []string{"Automatic verification plan:", "Run automatic verification now?", "a=auto-run"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected piped verification prompt output to contain %q, got %q", want, output)
		}
	}
}

func TestRuntimeStatePromptConfirmAutoVerifyNonInteractiveEOFDeclinesAfterPrompt(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		reader:      bufio.NewReader(strings.NewReader("")),
		writer:      &out,
		ui:          UI{},
		cfg:         Config{AutoLocale: boolPtr(false)},
		interactive: false,
		perms:       NewPermissionManager(ModeDefault, nil),
	}

	ok, err := rt.promptConfirmAutoVerify(VerificationPlan{
		Mode: VerificationAdaptive,
		Steps: []VerificationStep{{
			Label:   "focused verification",
			Command: "go test ./...",
			Status:  VerificationPending,
		}},
	})
	if err != nil {
		t.Fatalf("promptConfirmAutoVerify returned error: %v", err)
	}
	if ok {
		t.Fatalf("expected EOF verification prompt to decline verification")
	}
	output := out.String()
	if !strings.Contains(output, "Run automatic verification now?") {
		t.Fatalf("expected EOF verification prompt to be visible, got %q", output)
	}
}

func TestConfirmLabelUsesAutoAcceptHintForDiffPreview(t *testing.T) {
	rt := &runtimeState{
		ui:  UI{},
		cfg: Config{AutoLocale: boolPtr(false)},
	}

	label := rt.confirmLabel("Open diff preview?")
	if !strings.Contains(label, "a=auto-accept") {
		t.Fatalf("expected diff preview hint to advertise auto-accept, got %q", label)
	}
}

func TestConfirmLabelLocalizesDiffPreviewHintForKoreanLocale(t *testing.T) {
	t.Setenv("LANG", "ko_KR.UTF-8")
	rt := &runtimeState{
		ui:  UI{},
		cfg: Config{AutoLocale: boolPtr(true)},
	}

	label := rt.confirmLabel(diffPreviewQuestion(rt.cfg))
	for _, want := range []string{"변경 미리보기를 열까요?", "a=자동 수락", "Esc=취소"} {
		if !strings.Contains(label, want) {
			t.Fatalf("expected localized diff preview label to contain %q, got %q", want, label)
		}
	}
	if strings.Contains(label, "Open diff preview?") || strings.Contains(label, "auto-accept") || strings.Contains(label, "cancel") {
		t.Fatalf("expected localized diff preview label, got %q", label)
	}
}
