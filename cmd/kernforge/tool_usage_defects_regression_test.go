package main

// Permanent regression coverage for the four tool-layer defect clusters:
//   - apply_patch honesty/safety (T1, T7, T9)
//   - tool contract envelope/normalization (T2, T10, T13)
//   - run_shell chained-failure honesty (T3)
//   - grep truncation visibility (T4)
//   - tool exposure plan goal-draft policy (T11)
//   - MCP namespaced tool name collision avoidance (T5)
//
// These tests pin the corrected contract for each cluster so a future change
// that reintroduces the old (wrong) behavior fails loudly.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// T1: apply_patch "Add File" onto an existing regular file must fail closed and
// must NOT modify the existing file bytes.
func TestApplyPatchAddOntoExistingFileFailsClosed(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	original := "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ws := Workspace{BaseRoot: root, Root: root}
	tool := NewApplyPatchTool(ws)
	_, err := tool.Execute(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Add File: main.go\n+package main\n+// overwritten\n*** End Patch\n",
	})
	if err == nil {
		t.Fatalf("expected add-onto-existing-file to fail, got nil error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected already-exists error, got %v", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	if string(data) != original {
		t.Fatalf("add must not modify the existing file; got %q want %q", string(data), original)
	}
}

// T7: an insertion-only hunk (@@ -N,0 +N,k @@) must insert at the target line,
// not at end-of-file.
func TestApplyPatchInsertionOnlyHunkInsertsAtTargetLine(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	original := "line1\nline2\nline3\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ws := Workspace{BaseRoot: root, Root: root}
	tool := NewApplyPatchTool(ws)
	// Insert "inserted" before line2 (target line 2). No context/removal lines.
	_, err := tool.Execute(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: main.go\n@@ -2,0 +2,1 @@\n+inserted\n*** End Patch\n",
	})
	if err != nil {
		t.Fatalf("apply_patch insertion: %v", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	want := "line1\ninserted\nline2\nline3\n"
	if string(data) != want {
		t.Fatalf("insertion landed at wrong location; got %q want %q", string(data), want)
	}
}

// T9: patching a file with no trailing newline must preserve the
// no-trailing-newline state.
func TestApplyPatchPreservesMissingTrailingNewline(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	original := "alpha\nbeta" // no trailing newline
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ws := Workspace{BaseRoot: root, Root: root}
	tool := NewApplyPatchTool(ws)
	_, err := tool.Execute(context.Background(), map[string]any{
		"patch": "*** Begin Patch\n*** Update File: main.go\n@@\n alpha\n-beta\n+gamma\n*** End Patch\n",
	})
	if err != nil {
		t.Fatalf("apply_patch update: %v", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	want := "alpha\ngamma" // still no trailing newline
	if string(data) != want {
		t.Fatalf("trailing-newline state not preserved; got %q want %q", string(data), want)
	}
	if strings.HasSuffix(string(data), "\n") {
		t.Fatalf("file gained a trailing newline it never had: %q", string(data))
	}
}

// T2: ValidateToolCallsAgainstEnvelope must block run_shell with a JSON-array
// "command" (a type-mismatched git push) under a no-git envelope, and must still
// classify a normal string command correctly.
func TestValidateEnvelopeBlocksArrayShellGitPushUnderNoGit(t *testing.T) {
	registry := NewToolRegistry(&toolContractRecordingTool{name: "run_shell"})
	// no-git, but file mutation and web allowed, so only the git boundary is in play.
	noGit := RequestEnvelope{
		AllowsToolExecution: true,
		AllowsFileMutation:  true,
		AllowsGitMutation:   false,
		AllowsWebResearch:   true,
	}
	noGit.Normalize()

	// Array command: ["git","push"] passed where a string is expected. This reads
	// back empty in the boundary detectors, so it must fail CLOSED (blocked).
	arrayPush := ValidateToolCallsAgainstEnvelope([]ToolCall{{
		ID:        "call_array_push",
		Name:      "run_shell",
		Arguments: `{"command":["git","push"]}`,
	}}, noGit, ToolContractValidationOptions{Registry: registry})
	if len(arrayPush) < 1 {
		t.Fatalf("array git push under no-git envelope must be blocked (blocked>=1), got %#v", arrayPush)
	}
	if arrayPush[0].Kind != ToolContractSyntheticBlocked {
		t.Fatalf("expected blocked kind for array git push, got %#v", arrayPush[0])
	}

	// A normal read-only string command must not be blocked under the same envelope.
	stringList := ValidateToolCallsAgainstEnvelope([]ToolCall{{
		ID:        "call_string_status",
		Name:      "run_shell",
		Arguments: `{"command":"echo hello"}`,
	}}, noGit, ToolContractValidationOptions{Registry: registry})
	if len(stringList) != 0 {
		t.Fatalf("benign string command must classify clean under no-git envelope, got %#v", stringList)
	}

	// A string git push, however, must still be blocked.
	stringPush := ValidateToolCallsAgainstEnvelope([]ToolCall{{
		ID:        "call_string_push",
		Name:      "run_shell",
		Arguments: `{"command":"git push origin main"}`,
	}}, noGit, ToolContractValidationOptions{Registry: registry})
	if len(stringPush) != 1 || stringPush[0].Kind != ToolContractSyntheticBlocked {
		t.Fatalf("string git push under no-git envelope must be blocked, got %#v", stringPush)
	}
}

// T10: two tool calls colliding on an id (including a model-supplied
// X__duplicate_2 that would otherwise collide with the generated suffix) must
// end up with distinct ids so no tool result is dropped.
func TestNormalizeAssistantToolCallsDeduplicatesColludingIDs(t *testing.T) {
	registry := NewToolRegistry(&toolContractRecordingTool{name: "read_file", readOnly: true})
	normalized := NormalizeAssistantToolCalls([]ToolCall{
		{ID: "dup", Name: "read_file", Arguments: `{}`},
		{ID: "dup", Name: "read_file", Arguments: `{}`},
		// Model pre-supplies the exact id the dedup loop would otherwise mint.
		{ID: "dup__duplicate_2", Name: "read_file", Arguments: `{}`},
	}, ToolContractNormalizationOptions{Registry: registry})
	if len(normalized.Calls) != 3 {
		t.Fatalf("expected three normalized calls, got %#v", normalized.Calls)
	}
	seen := map[string]int{}
	for _, call := range normalized.Calls {
		if strings.TrimSpace(call.ID) == "" {
			t.Fatalf("normalized call has empty id: %#v", call)
		}
		seen[call.ID]++
	}
	if len(seen) != 3 {
		t.Fatalf("expected three distinct ids, got %#v", seen)
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("id %q reused %d times; ids must be unique", id, count)
		}
	}
}

// T13: a tool call whose Arguments is literally "null" must be flagged invalid
// instead of silently accepted as an empty object.
func TestNormalizeAssistantToolCallsFlagsNullArguments(t *testing.T) {
	registry := NewToolRegistry(&toolContractRecordingTool{name: "read_file", readOnly: true})
	normalized := NormalizeAssistantToolCalls([]ToolCall{
		{ID: "call_null", Name: "read_file", Arguments: "null"},
	}, ToolContractNormalizationOptions{Registry: registry})
	var invalid *ToolContractSyntheticResult
	for i := range normalized.SyntheticResults {
		if normalized.SyntheticResults[i].Call.ID == "call_null" &&
			normalized.SyntheticResults[i].Kind == ToolContractSyntheticInvalid {
			invalid = &normalized.SyntheticResults[i]
			break
		}
	}
	if invalid == nil {
		t.Fatalf("Arguments==null must produce an invalid synthetic result, got %#v", normalized.SyntheticResults)
	}
	if !strings.Contains(strings.ToLower(invalid.Reason), "invalid") {
		t.Fatalf("expected INVALID reason for null arguments, got %q", invalid.Reason)
	}
}

// T3: run_shell of a PowerShell chain where an earlier command fails must report
// failure / non-passed. Guarded to PowerShell-capable Windows environments to
// stay deterministic.
func TestRunShellPowerShellChainEarlyFailureReportsFailure(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("powershell chain failure test requires Windows")
	}
	root := t.TempDir()
	ws := Workspace{BaseRoot: root, Root: root, Shell: "powershell"}
	tool := NewRunShellTool(ws)
	// First command fails (exit 3); chained with && so the chain must not pass.
	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"command": "cmd /c exit 3; if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }; Write-Output ok",
	})
	if err == nil {
		t.Fatalf("expected failure from an early-failing powershell chain, got nil error")
	}
	if status, ok := result.Meta["command_execution_status"]; ok {
		if str, _ := status.(string); strings.EqualFold(str, "completed") || strings.EqualFold(str, "passed") {
			t.Fatalf("failed chain must not report a passed/completed status, got %v", status)
		}
	}
	if exit, ok := result.Meta["exit_code"]; ok {
		if code, _ := exit.(int); code == 0 {
			t.Fatalf("failed chain must not report exit_code 0")
		}
	}
}

// T4: grep that hits max_results must include a truncation notice in the
// model-visible text.
func TestGrepMaxResultsIncludesTruncationNotice(t *testing.T) {
	root := t.TempDir()
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString("needle here\n")
	}
	if err := os.WriteFile(filepath.Join(root, "hits.txt"), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ws := Workspace{BaseRoot: root, Root: root}
	tool := NewGrepTool(ws)
	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"pattern":     "needle",
		"max_results": 5,
	})
	if err != nil {
		t.Fatalf("grep: %v", err)
	}
	if !strings.Contains(result.DisplayText, "truncated") {
		t.Fatalf("grep at max_results must include a truncation notice, got:\n%s", result.DisplayText)
	}
	if truncated, _ := result.Meta["truncated"].(bool); !truncated {
		t.Fatalf("grep meta must flag truncated=true, got %#v", result.Meta["truncated"])
	}
}

// T11: a goal-prompt-draft-only envelope must disable create_goal/update_goal in
// the exposure plan.
func TestTurnToolExposurePlanDisablesGoalToolsForGoalDraftOnly(t *testing.T) {
	registry := NewToolRegistry(
		&toolContractRecordingTool{name: "create_goal"},
		&toolContractRecordingTool{name: "update_goal"},
		&toolContractRecordingTool{name: "read_file", readOnly: true},
	)
	agent := &Agent{Tools: registry}
	envelope := RequestEnvelope{
		ExternalUserText:    "다음 작업을 위한 goal 프롬프트 초안만 작성해줘",
		AllowsToolExecution: true,
		AllowsFileMutation:  true,
		AllowsGitMutation:   false,
		AllowsWebResearch:   true,
		GoalPromptDraftOnly: true,
	}
	plan := agent.buildTurnToolExposurePlanForEnvelope(map[string]bool{}, envelope, false, false, false, false, true, false)
	if !plan.toolDisabled("create_goal") {
		t.Fatalf("goal-prompt-draft-only turn must disable create_goal, got %#v", plan.DisabledTools)
	}
	if !plan.toolDisabled("update_goal") {
		t.Fatalf("goal-prompt-draft-only turn must disable update_goal, got %#v", plan.DisabledTools)
	}
	if plan.toolDisabled("read_file") {
		t.Fatalf("read_file must remain available for a goal-draft turn, got %#v", plan.DisabledTools)
	}
}

// T5: two distinct MCP (server, tool) pairs that sanitize to the same namespaced
// name must each receive a distinct registered name.
func TestResolveNamespacedMCPToolNamesDisambiguatesCollisions(t *testing.T) {
	// "srv.a" and "srv-a" both sanitize to "srv_a"; tools "do.it" and "do-it"
	// both sanitize to "do_it", so the naive namespaced names collide.
	serverA := &MCPClient{
		config: MCPServerConfig{Name: "srv.a"},
		tools:  []MCPToolDescriptor{{Name: "do.it"}},
	}
	serverB := &MCPClient{
		config: MCPServerConfig{Name: "srv-a"},
		tools:  []MCPToolDescriptor{{Name: "do-it"}},
	}
	resolved, _ := resolveNamespacedMCPToolNames([]*MCPClient{serverA, serverB})
	nameA := resolved[serverA][0]
	nameB := resolved[serverB][0]
	if nameA == "" || nameB == "" {
		t.Fatalf("expected resolved names for both servers, got %q and %q", nameA, nameB)
	}
	if nameA == nameB {
		t.Fatalf("colliding MCP tool identities must get distinct registered names, both = %q", nameA)
	}
}
