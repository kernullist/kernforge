package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

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
	if shouldAnimateThinkingStatus("Using read_file on TavernWorkerCore.cpp:1-200...") {
		t.Fatalf("expected specific tool progress to stop spinner animation")
	}
	if shouldAnimateThinkingStatus("read_file 확인 중 ... TavernWorkerCore.cpp") {
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
		PrimaryStartup: "Tavern",
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

func TestRuntimeStatePrintAssistantWhileThinkingUsesNextActivityLine(t *testing.T) {
	var out bytes.Buffer
	rt := &runtimeState{
		writer: &out,
		ui:     UI{},
	}

	rt.printAssistantWhileThinking("I am going to inspect the auth flow first.")

	rendered := out.String()
	if !strings.Contains(rendered, "[next") {
		t.Fatalf("expected assistant preamble to render as next-step activity, got %q", rendered)
	}
	if !strings.Contains(rendered, "inspect the auth flow first") {
		t.Fatalf("expected activity line to include assistant preamble text, got %q", rendered)
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
		cfg:         DefaultConfig(home),
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

	output := &bytes.Buffer{}
	rt := &runtimeState{
		reader:      bufio.NewReader(strings.NewReader("a\n")),
		writer:      output,
		ui:          UI{},
		interactive: true,
		cfg:         DefaultConfig(workspace),
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
		cfg:         DefaultConfig(workspace),
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
		perms:     NewPermissionManager(ModeDefault, nil),
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
	if !strings.Contains(text, "Effective settings merged from config files, environment, and session overrides.") {
		t.Fatalf("expected config hint, got %q", text)
	}
	if !strings.Contains(text, "auto_checkpoint_edits:") {
		t.Fatalf("expected config settings in config output, got %q", text)
	}
	if !strings.Contains(text, "hooks_enabled:") {
		t.Fatalf("expected hook settings in config output, got %q", text)
	}
	if !strings.Contains(text, "-- Model ") || !strings.Contains(text, "-- Tool Paths ") || !strings.Contains(text, "-- Extensions ") {
		t.Fatalf("expected grouped config output, got %q", text)
	}
	if strings.Contains(text, "write_approval:") {
		t.Fatalf("did not expect runtime-only approval state in config, got %q", text)
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

func TestConfirmLabelUsesAutoAcceptHintForDiffPreview(t *testing.T) {
	rt := &runtimeState{
		ui: UI{},
	}

	label := rt.confirmLabel("Open diff preview?")
	if !strings.Contains(label, "a=auto-accept") {
		t.Fatalf("expected diff preview hint to advertise auto-accept, got %q", label)
	}
}
