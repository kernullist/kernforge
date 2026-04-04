package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestShouldCancelOnEscapeRequiresForegroundTarget(t *testing.T) {
	called := false
	allow := func() bool {
		called = true
		return true
	}

	if shouldCancelOnEscape(false, allow) {
		t.Fatalf("expected cancel to be ignored without foreground target")
	}
	if called {
		t.Fatalf("expected shouldCancel callback to be skipped without foreground target")
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

func TestShouldCancelOnRepeatedEscapeAllowsFallbackWithoutForegroundTarget(t *testing.T) {
	called := false
	allow := func() bool {
		called = true
		return true
	}

	if !shouldCancelOnRepeatedEscape(false, true, allow) {
		t.Fatalf("expected repeated escape fallback to allow cancellation")
	}
	if !called {
		t.Fatalf("expected cancel gate to be consulted")
	}
}

func TestShouldCancelOnRepeatedEscapeStillHonorsRuntimeGate(t *testing.T) {
	called := false
	allow := func() bool {
		called = true
		return false
	}

	if shouldCancelOnRepeatedEscape(false, true, allow) {
		t.Fatalf("expected repeated escape fallback to honor runtime gate")
	}
	if !called {
		t.Fatalf("expected cancel gate to be consulted")
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
	if strings.Count(rendered, "assistant:") != 1 {
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
	if strings.Count(rendered, "assistant:") != 1 {
		t.Fatalf("expected punctuation-only variant to be suppressed, got %q", rendered)
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
