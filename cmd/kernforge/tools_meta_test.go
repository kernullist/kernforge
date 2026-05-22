package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type mutableRegistryTool struct {
	def    ToolDefinition
	output string
	hidden bool
}

func (t *mutableRegistryTool) Definition() ToolDefinition {
	if t == nil {
		return ToolDefinition{}
	}
	return t.def
}

func (t *mutableRegistryTool) Execute(ctx context.Context, input any) (string, error) {
	if t == nil {
		return "", fmt.Errorf("nil mutable registry tool")
	}
	return t.output, nil
}

func (t *mutableRegistryTool) HiddenFromModel() bool {
	return t != nil && t.hidden
}

func TestToolRegistrySnapshotsDefinitionsAtRegistration(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type": "string",
			},
		},
	}
	tool := &mutableRegistryTool{
		def: ToolDefinition{
			Name:        " mutable ",
			Description: "registered description",
			InputSchema: schema,
		},
		output: "ok",
	}
	registry := NewToolRegistry(tool)

	tool.def.Description = "mutated description"
	schema["type"] = "array"
	schema["properties"].(map[string]any)["path"].(map[string]any)["type"] = "number"

	defs := registry.Definitions()
	if len(defs) != 1 {
		t.Fatalf("expected one definition, got %d", len(defs))
	}
	if defs[0].Name != "mutable" {
		t.Fatalf("expected trimmed registered name, got %q", defs[0].Name)
	}
	if defs[0].Description != "registered description" {
		t.Fatalf("definition changed after registration: %q", defs[0].Description)
	}
	properties := defs[0].InputSchema["properties"].(map[string]any)
	pathSchema := properties["path"].(map[string]any)
	if defs[0].InputSchema["type"] != "object" || pathSchema["type"] != "string" {
		t.Fatalf("schema was not snapshotted: %#v", defs[0].InputSchema)
	}

	defs[0].Description = "caller mutation"
	defs[0].InputSchema["type"] = "caller mutation"

	defs = registry.Definitions()
	if defs[0].Description != "registered description" || defs[0].InputSchema["type"] != "object" {
		t.Fatalf("returned definitions must not mutate registry snapshot: %#v", defs[0])
	}

	result, err := registry.ExecuteDetailed(context.Background(), "mutable", `{}`)
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if result.DisplayText != "ok" {
		t.Fatalf("expected registered tool to execute, got %q", result.DisplayText)
	}
}

func TestToolRegistryIgnoresInvalidAndDuplicateDefinitions(t *testing.T) {
	var nilTool *mutableRegistryTool
	first := &mutableRegistryTool{
		def: ToolDefinition{
			Name:        "dup",
			Description: "first",
			InputSchema: emptyObjectSchema(),
		},
		output: "first output",
	}
	second := &mutableRegistryTool{
		def: ToolDefinition{
			Name:        "dup",
			Description: "second",
			InputSchema: emptyObjectSchema(),
		},
		output: "second output",
	}
	missingSchema := &mutableRegistryTool{
		def: ToolDefinition{
			Name:        "missing_schema",
			Description: "missing schema",
		},
		output: "missing schema output",
	}
	blank := &mutableRegistryTool{
		def: ToolDefinition{
			Name: "   ",
		},
		output: "blank output",
	}
	invalidSchema := &mutableRegistryTool{
		def: ToolDefinition{
			Name: "invalid_schema",
			InputSchema: map[string]any{
				"type": "null",
			},
		},
		output: "invalid output",
	}
	invalidNestedSchema := &mutableRegistryTool{
		def: ToolDefinition{
			Name: "invalid_nested_schema",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": "not a schema object",
				},
			},
		},
		output: "invalid nested output",
	}

	registry := NewToolRegistry(nilTool, first, second, missingSchema, blank, invalidSchema, invalidNestedSchema)
	defs := registry.Definitions()
	if len(defs) != 1 {
		t.Fatalf("expected only first valid unique tool definition, got %#v", defs)
	}
	if defs[0].Name != "dup" || defs[0].Description != "first" {
		t.Fatalf("duplicate handling should keep the first registered spec, got %#v", defs[0])
	}

	result, err := registry.ExecuteDetailed(context.Background(), "dup", `{}`)
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if result.DisplayText != "first output" {
		t.Fatalf("duplicate handling should keep first executor, got %q", result.DisplayText)
	}
	if _, err := registry.ExecuteDetailed(context.Background(), "invalid_schema", `{}`); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("invalid schema tool should not be registered, got %v", err)
	}
	if _, err := registry.ExecuteDetailed(context.Background(), "invalid_nested_schema", `{}`); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("invalid nested schema tool should not be registered, got %v", err)
	}
	if _, err := registry.ExecuteDetailed(context.Background(), "missing_schema", `{}`); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("missing schema tool should not be registered, got %v", err)
	}
	issues := registry.RegistrationIssues()
	if len(issues) != 5 {
		t.Fatalf("expected duplicate and invalid definitions to be reported, got %#v", issues)
	}
	issueText := strings.Join(formatToolRegistrationIssues(issues), "\n")
	for _, want := range []string{
		"dup: duplicate tool name",
		"missing_schema: missing input schema",
		"missing tool name",
		"invalid_schema: invalid input schema",
		"invalid_nested_schema: invalid input schema",
	} {
		if !strings.Contains(issueText, want) {
			t.Fatalf("expected registration issue %q, got:\n%s", want, issueText)
		}
	}

	issues[0].Reason = "caller mutation"
	if registry.RegistrationIssues()[0].Reason == "caller mutation" {
		t.Fatalf("registration issues must be returned as a copy")
	}
}

func TestToolRegistryNormalizesObjectSchemasBeforeExposure(t *testing.T) {
	tool := &mutableRegistryTool{
		def: ToolDefinition{
			Name: "missing_properties",
			InputSchema: map[string]any{
				"type": "object",
			},
		},
		output: "ok",
	}

	registry := NewToolRegistry(tool)
	defs := registry.Definitions()
	if len(defs) != 1 {
		t.Fatalf("expected normalized schema to remain visible, got %#v", defs)
	}
	properties, ok := defs[0].InputSchema["properties"].(map[string]any)
	if !ok || len(properties) != 0 {
		t.Fatalf("expected missing object properties to normalize to an empty object, got %#v", defs[0].InputSchema)
	}
	result, err := registry.ExecuteDetailed(context.Background(), "missing_properties", `{}`)
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if result.DisplayText != "ok" {
		t.Fatalf("expected normalized tool to remain dispatchable, got %q", result.DisplayText)
	}
}

func TestToolRegistryAcceptsRootAnyOfSchemas(t *testing.T) {
	tool := &mutableRegistryTool{
		def: ToolDefinition{
			Name: "union_root",
			InputSchema: map[string]any{
				"anyOf": []any{
					map[string]any{
						"type": "object",
						"properties": map[string]any{
							"kind": map[string]any{
								"const": "file",
							},
							"path": map[string]any{
								"type": "string",
							},
						},
						"required":             []any{"kind", "path"},
						"additionalProperties": false,
					},
					map[string]any{
						"type": "object",
						"properties": map[string]any{
							"kind": map[string]any{
								"const": "query",
							},
							"text": map[string]any{
								"type": "string",
							},
						},
						"required":             []any{"kind", "text"},
						"additionalProperties": false,
					},
				},
			},
		},
		output: "ok",
	}

	registry := NewToolRegistry(tool)
	if issues := registry.RegistrationIssues(); len(issues) != 0 {
		t.Fatalf("root anyOf schema should be accepted, got %#v", issues)
	}
	defs := registry.Definitions()
	if len(defs) != 1 {
		t.Fatalf("expected one visible definition, got %#v", defs)
	}
	branches := defs[0].InputSchema["anyOf"].([]any)
	firstProperties := branches[0].(map[string]any)["properties"].(map[string]any)
	kindSchema := firstProperties["kind"].(map[string]any)
	if _, hasConst := kindSchema["const"]; hasConst {
		t.Fatalf("const should be normalized before exposure, got %#v", kindSchema)
	}
	if _, hasEnum := kindSchema["enum"]; !hasEnum {
		t.Fatalf("const should normalize to enum, got %#v", kindSchema)
	}
}

func TestToolRegistryInfersAndSanitizesNestedSchemasBeforeExposure(t *testing.T) {
	tool := &mutableRegistryTool{
		def: ToolDefinition{
			Name: "inferred_object",
			InputSchema: map[string]any{
				"properties": map[string]any{
					"query": map[string]any{
						"description": "search query",
					},
					"tags": map[string]any{
						"type": "array",
					},
					"mode": map[string]any{
						"const": "fast",
					},
				},
			},
		},
		output: "ok",
	}

	registry := NewToolRegistry(tool)
	defs := registry.Definitions()
	if len(defs) != 1 {
		t.Fatalf("expected inferred schema to remain visible, got %#v", defs)
	}
	if defs[0].InputSchema["type"] != "object" {
		t.Fatalf("expected top-level properties to infer object type, got %#v", defs[0].InputSchema)
	}
	properties := defs[0].InputSchema["properties"].(map[string]any)
	query := properties["query"].(map[string]any)
	if query["type"] != "string" {
		t.Fatalf("expected property without type to default to string, got %#v", query)
	}
	tags := properties["tags"].(map[string]any)
	if _, ok := tags["items"].(map[string]any); !ok {
		t.Fatalf("expected array property without items to receive default items, got %#v", tags)
	}
	mode := properties["mode"].(map[string]any)
	enum, ok := mode["enum"].([]any)
	if !ok || len(enum) != 1 || enum[0] != "fast" {
		t.Fatalf("expected const to be rewritten as a single-value enum, got %#v", mode)
	}
	if _, ok := mode["const"]; ok {
		t.Fatalf("expected const keyword to be removed after normalization, got %#v", mode)
	}
}

func TestToolRegistrySupportsHiddenDispatchOnlyTools(t *testing.T) {
	visible := &mutableRegistryTool{
		def: ToolDefinition{
			Name:        "visible",
			Description: "visible tool",
			InputSchema: emptyObjectSchema(),
		},
		output: "visible output",
	}
	hidden := &mutableRegistryTool{
		def: ToolDefinition{
			Name:        "hidden",
			Description: "hidden tool",
			InputSchema: emptyObjectSchema(),
		},
		output: "hidden output",
		hidden: true,
	}

	registry := NewToolRegistry(visible, hidden)
	defs := registry.Definitions()
	if len(defs) != 1 || defs[0].Name != "visible" {
		t.Fatalf("expected only visible definition, got %#v", defs)
	}
	if !toolRegistryHasTool(registry, "hidden") {
		t.Fatalf("hidden tool should remain dispatchable")
	}
	if got := registry.ToolNames(); strings.Join(got, ",") != "hidden,visible" {
		t.Fatalf("expected all dispatchable tool names, got %#v", got)
	}
	result, err := registry.ExecuteDetailed(context.Background(), "hidden", `{}`)
	if err != nil {
		t.Fatalf("hidden ExecuteDetailed: %v", err)
	}
	if result.DisplayText != "hidden output" {
		t.Fatalf("expected hidden executor output, got %q", result.DisplayText)
	}
}

func TestReadFileExecuteDetailedReturnsStructuredMeta(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.txt")
	if err := os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewReadFileTool(Workspace{BaseRoot: root, Root: root})
	registry := NewToolRegistry(tool)

	result, err := registry.ExecuteDetailed(context.Background(), "read_file", `{"path":"sample.txt","start_line":2,"end_line":3}`)
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if toolMetaString(result.Meta, "effect") != "inspect" {
		t.Fatalf("expected inspect effect, got %#v", result.Meta)
	}
	if toolMetaString(result.Meta, "path") != "sample.txt" {
		t.Fatalf("expected relative path metadata, got %#v", result.Meta)
	}
	if toolMetaInt(result.Meta, "line_count") != 2 {
		t.Fatalf("expected line count 2, got %#v", result.Meta)
	}
	if toolMetaString(result.Meta, "cache_mode") != "fresh" {
		t.Fatalf("expected fresh cache mode, got %#v", result.Meta)
	}
}

func TestSkippedVerificationPollDoesNotAdvancePlan(t *testing.T) {
	tests := []struct {
		name string
		call ToolCall
		meta map[string]any
	}{
		{
			name: "job",
			call: ToolCall{Name: "check_shell_job"},
			meta: map[string]any{
				"verification_like":           true,
				"verification_status":         string(VerificationSkipped),
				"command_execution_status":    "declined",
				"job_status":                  "completed",
				"verification_command_source": "automatic",
			},
		},
		{
			name: "bundle",
			call: ToolCall{Name: "check_shell_bundle"},
			meta: map[string]any{
				"verification_like":           true,
				"verification_status":         string(VerificationSkipped),
				"command_execution_status":    "declined",
				"bundle_status":               "failed",
				"verification_command_source": "automatic",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ToolExecutionResult{Meta: tt.meta}
			outcome := buildToolExecutionPolicy(tt.call, result, nil)
			if outcome.ResultClass != "verification_skipped" {
				t.Fatalf("expected skipped verification result class, got %#v", outcome)
			}
			if outcome.PlanEffect != "none" {
				t.Fatalf("skipped verification poll must not advance the plan, got %#v", outcome)
			}
		})
	}
}

func TestReadFileExecuteDetailedReturnsMissingPathHint(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "analysis"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	tool := NewReadFileTool(Workspace{BaseRoot: root, Root: root})
	registry := NewToolRegistry(tool)

	result, err := registry.ExecuteDetailed(context.Background(), "read_file", `{"path":"analysis/security-review.md"}`)
	if err == nil {
		t.Fatalf("expected missing file error")
	}
	if !strings.Contains(result.DisplayText, "read_file target does not exist: analysis/security-review.md") {
		t.Fatalf("expected missing path hint, got %q", result.DisplayText)
	}
	if !strings.Contains(result.DisplayText, "Parent directory exists but is empty.") {
		t.Fatalf("expected empty parent hint, got %q", result.DisplayText)
	}
	if toolMetaString(result.Meta, "error_kind") != "not_found" {
		t.Fatalf("expected not_found meta, got %#v", result.Meta)
	}
}

func TestRunShellExecuteDetailedReturnsStructuredMeta(t *testing.T) {
	root := t.TempDir()
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
		Shell:    defaultShell(),
	}
	registry := NewToolRegistry(NewRunShellTool(ws))
	command := "echo alpha"
	if runtime.GOOS == "windows" {
		command = "Write-Output alpha"
	}

	jsonInput := `{"command":"` + strings.ReplaceAll(command, `\`, `\\`) + `"}`
	result, err := registry.ExecuteDetailed(context.Background(), "run_shell", jsonInput)
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if toolMetaString(result.Meta, "mutation_class") != string(shellMutationReadOnly) {
		t.Fatalf("expected read_only mutation class, got %#v", result.Meta)
	}
	if !toolMetaBool(result.Meta, "success") {
		t.Fatalf("expected success flag, got %#v", result.Meta)
	}
	if toolMetaString(result.Meta, "command") == "" {
		t.Fatalf("expected command metadata, got %#v", result.Meta)
	}
}

func TestRunShellExecuteDetailedIncludesEffectiveWorkspaceRoots(t *testing.T) {
	baseRoot := t.TempDir()
	activeRoot := filepath.Join(baseRoot, "worktree")
	if err := os.MkdirAll(activeRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	ws := Workspace{
		BaseRoot: baseRoot,
		Root:     activeRoot,
		Shell:    defaultShell(),
	}
	registry := NewToolRegistry(NewRunShellTool(ws))
	command := "echo roots"
	if runtime.GOOS == "windows" {
		command = "Write-Output roots"
	}

	payload, err := json.Marshal(map[string]any{"command": command})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result, err := registry.ExecuteDetailed(context.Background(), "run_shell", string(payload))
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if got := toolMetaString(result.Meta, "workspace_root"); !sameFilePath(got, baseRoot) {
		t.Fatalf("expected base workspace_root %q, got %#v", baseRoot, result.Meta)
	}
	if got := toolMetaString(result.Meta, "active_workspace_root"); !sameFilePath(got, activeRoot) {
		t.Fatalf("expected active workspace root %q, got %#v", activeRoot, result.Meta)
	}
	if got := toolMetaString(result.Meta, "work_dir"); !sameFilePath(got, activeRoot) {
		t.Fatalf("expected shell work_dir %q, got %#v", activeRoot, result.Meta)
	}
	roots := toolMetaStringSlice(result.Meta, "workspace_roots")
	if len(roots) != 2 || !sameFilePath(roots[0], baseRoot) || !sameFilePath(roots[1], activeRoot) {
		t.Fatalf("expected effective workspace_roots [%q %q], got %#v", baseRoot, activeRoot, result.Meta)
	}
}

func TestRunShellExecuteDetailedIncludesActivePermissionProfile(t *testing.T) {
	root := t.TempDir()
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
		Shell:    defaultShell(),
		Perms:    NewPermissionManager(ModeBypass, nil),
	}
	registry := NewToolRegistry(NewRunShellTool(ws))
	command := "echo permissions"
	if runtime.GOOS == "windows" {
		command = "Write-Output permissions"
	}

	payload, err := json.Marshal(map[string]any{"command": command})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result, err := registry.ExecuteDetailed(context.Background(), "run_shell", string(payload))
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if got := toolMetaString(result.Meta, "permission_mode"); got != string(ModeBypass) {
		t.Fatalf("expected permission mode %q, got %#v", ModeBypass, result.Meta)
	}
	if got := toolMetaString(result.Meta, "active_permission_profile_id"); got != builtInPermissionProfileDangerFullAccess {
		t.Fatalf("expected active permission profile id %q, got %#v", builtInPermissionProfileDangerFullAccess, result.Meta)
	}
	activeProfile, ok := result.Meta["active_permission_profile"].(map[string]any)
	if !ok || activeProfile["id"] != builtInPermissionProfileDangerFullAccess {
		t.Fatalf("expected active permission profile snapshot, got %#v", result.Meta)
	}
	if got := toolMetaString(result.Meta, "sandbox"); got != "none" {
		t.Fatalf("expected sandbox tag to mirror unsandboxed execution, got %#v", result.Meta)
	}
}

func TestRunShellHookPayloadIncludesEffectiveWorkspaceRoots(t *testing.T) {
	baseRoot := t.TempDir()
	activeRoot := filepath.Join(baseRoot, "worktree")
	if err := os.MkdirAll(activeRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	var prePayload HookPayload
	ws := Workspace{
		BaseRoot: baseRoot,
		Root:     activeRoot,
		Shell:    defaultShell(),
		Perms: NewPermissionManager(ModeDefault, func(string) (bool, error) {
			return true, nil
		}),
		RunHook: func(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
			if event == HookPreToolUse {
				prePayload = payload
			}
			return HookVerdict{Allow: true}, nil
		},
	}
	registry := NewToolRegistry(NewRunShellTool(ws))
	command := "echo roots"
	if runtime.GOOS == "windows" {
		command = "Write-Output roots"
	}
	payload, err := json.Marshal(map[string]any{"command": command})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := registry.ExecuteDetailed(context.Background(), "run_shell", string(payload)); err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if got := toolMetaString(prePayload, "workspace_root"); !sameFilePath(got, baseRoot) {
		t.Fatalf("expected hook workspace_root %q, got %#v", baseRoot, prePayload)
	}
	if got := toolMetaString(prePayload, "active_workspace_root"); !sameFilePath(got, activeRoot) {
		t.Fatalf("expected hook active workspace root %q, got %#v", activeRoot, prePayload)
	}
	if got := toolMetaString(prePayload, "work_dir"); !sameFilePath(got, activeRoot) {
		t.Fatalf("expected hook work_dir %q, got %#v", activeRoot, prePayload)
	}
	roots := toolMetaStringSlice(prePayload, "workspace_roots")
	if len(roots) != 2 || !sameFilePath(roots[0], baseRoot) || !sameFilePath(roots[1], activeRoot) {
		t.Fatalf("expected hook workspace_roots [%q %q], got %#v", baseRoot, activeRoot, prePayload)
	}
	if got := toolMetaString(prePayload, "permission_mode"); got != string(ModeDefault) {
		t.Fatalf("expected hook permission mode %q, got %#v", ModeDefault, prePayload)
	}
	if got := toolMetaString(prePayload, "active_permission_profile_id"); got != builtInPermissionProfileWorkspace {
		t.Fatalf("expected hook active permission profile %q, got %#v", builtInPermissionProfileWorkspace, prePayload)
	}
}

func TestRunShellHookPayloadIncludesSpecialistAgentIdentity(t *testing.T) {
	root := t.TempDir()
	var prePayload HookPayload
	ws := Workspace{
		BaseRoot: root,
		Root:     root,
		Shell:    defaultShell(),
		Perms: NewPermissionManager(ModeDefault, func(string) (bool, error) {
			return true, nil
		}),
		ResolveShellRoot: func(ownerNodeID string) (ShellRoutingResult, error) {
			return ShellRoutingResult{
				Root:        root,
				OwnerNodeID: strings.TrimSpace(ownerNodeID),
				Specialist:  "driver-build-fixer",
			}, nil
		},
		RunHook: func(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
			if event == HookPreToolUse {
				prePayload = payload
			}
			return HookVerdict{Allow: true}, nil
		},
	}
	registry := NewToolRegistry(NewRunShellTool(ws))
	command := "echo specialist"
	if runtime.GOOS == "windows" {
		command = "Write-Output specialist"
	}
	payload, err := json.Marshal(map[string]any{
		"command":       command,
		"owner_node_id": "plan-02",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := registry.ExecuteDetailed(context.Background(), "run_shell", string(payload)); err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if got := toolMetaString(prePayload, "specialist"); got != "driver-build-fixer" {
		t.Fatalf("expected hook specialist, got %#v", prePayload)
	}
	if got := toolMetaString(prePayload, "agent_id"); got != "plan-02" {
		t.Fatalf("expected hook agent_id, got %#v", prePayload)
	}
	if got := toolMetaString(prePayload, "agent_type"); got != "driver-build-fixer" {
		t.Fatalf("expected hook agent_type, got %#v", prePayload)
	}
}

func TestBackgroundVerificationStartIsPendingNotEvidence(t *testing.T) {
	job := BackgroundShellJob{
		ID:             "job-1",
		Command:        "go test ./...",
		CommandSummary: "go test ./...",
		Status:         "running",
		MutationClass:  string(shellMutationVerificationArtifacts),
	}

	meta := buildBackgroundJobMeta(job, nil, map[string]any{
		"tool_name":    "run_shell_background",
		"result_class": "background_start",
	})
	if got := toolMetaExplicitVerificationStatus(meta); got != VerificationPending {
		t.Fatalf("expected pending verification status, got %q meta=%#v", got, meta)
	}
	if toolMetaBool(meta, "verification_evidence") {
		t.Fatalf("background verification start must not be successful evidence: %#v", meta)
	}
	if toolResultHasSuccessfulVerificationEvidence("run_shell_background", meta, "started background shell job job-1 [running]") {
		t.Fatalf("background verification start must not satisfy verification evidence")
	}
}

func TestCompletedBackgroundVerificationCanBeEvidence(t *testing.T) {
	exitCode := 0
	job := BackgroundShellJob{
		ID:             "job-1",
		Command:        "go test ./...",
		CommandSummary: "go test ./...",
		Status:         "completed",
		MutationClass:  string(shellMutationVerificationArtifacts),
		ExitCode:       &exitCode,
	}

	meta := buildBackgroundJobMeta(job, nil, map[string]any{
		"tool_name":    "check_shell_job",
		"result_class": "background_status",
	})
	if got := toolMetaExplicitVerificationStatus(meta); got != VerificationPassed {
		t.Fatalf("expected passed verification status, got %q meta=%#v", got, meta)
	}
	if !toolMetaBool(meta, "verification_evidence") {
		t.Fatalf("completed zero-exit verification should be evidence: %#v", meta)
	}
	if !toolResultHasSuccessfulVerificationEvidence("check_shell_job", meta, "exit_code: 0") {
		t.Fatalf("completed zero-exit background verification should satisfy evidence")
	}
}

func TestUpdatePlanExecuteDetailedReturnsCounts(t *testing.T) {
	root := t.TempDir()
	var captured []PlanItem
	tool := NewUpdatePlanTool(Workspace{
		BaseRoot: root,
		Root:     root,
		UpdatePlan: func(items []PlanItem) {
			captured = append([]PlanItem(nil), items...)
		},
	})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"items": []any{
			map[string]any{"step": "Inspect", "status": "completed"},
			map[string]any{"step": "Fix", "status": "in_progress"},
			map[string]any{"step": "Verify", "status": "pending"},
		},
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if len(captured) != 3 {
		t.Fatalf("expected captured plan items, got %#v", captured)
	}
	if toolMetaString(result.Meta, "effect") != "plan" {
		t.Fatalf("expected plan effect, got %#v", result.Meta)
	}
	if toolMetaInt(result.Meta, "completed_count") != 1 || toolMetaInt(result.Meta, "in_progress_count") != 1 || toolMetaInt(result.Meta, "pending_count") != 1 {
		t.Fatalf("expected balanced plan counts, got %#v", result.Meta)
	}
}

func TestApplyPatchExecuteDetailedReturnsChangedPathsMeta(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			return true, nil
		},
	})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"patch": strings.Join([]string{
			"*** Begin Patch",
			"*** Update File: main.go",
			"@@",
			" package main",
			"+func main() {}",
			"*** End Patch",
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	changedPaths := toolMetaStringSlice(result.Meta, "changed_paths")
	if len(changedPaths) != 1 || changedPaths[0] != "main.go" {
		t.Fatalf("expected changed path metadata, got %#v", result.Meta)
	}
	if toolMetaString(result.Meta, "effect") != "edit" {
		t.Fatalf("expected edit effect, got %#v", result.Meta)
	}
	unifiedDiff := toolMetaString(result.Meta, "unified_diff")
	for _, want := range []string{
		"diff --git a/main.go b/main.go",
		"--- a/main.go",
		"+++ b/main.go",
		"+func main() {}",
	} {
		if !strings.Contains(unifiedDiff, want) {
			t.Fatalf("expected unified diff to contain %q, got %q", want, unifiedDiff)
		}
	}
}

func TestWriteFileExecuteDetailedIncludesUnifiedDiff(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewWriteFileTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			return true, nil
		},
	})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"path":    "main.go",
		"content": "package main\n\nfunc main() {}\n",
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if got := toolMetaString(result.Meta, "path"); got != "main.go" {
		t.Fatalf("expected display path metadata, got %#v", result.Meta)
	}
	unifiedDiff := toolMetaString(result.Meta, "unified_diff")
	for _, want := range []string{
		"diff --git a/main.go b/main.go",
		"--- a/main.go",
		"+++ b/main.go",
		"+func main() {}",
	} {
		if !strings.Contains(unifiedDiff, want) {
			t.Fatalf("expected unified diff to contain %q, got %q", want, unifiedDiff)
		}
	}
}

func TestWriteFileExecuteDetailedReportsNoWorkspaceChangeForNoOp(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewWriteFileTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			t.Fatalf("no-op write_file must not request edit preview: %#v", preview)
			return false, nil
		},
	})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"path":    "main.go",
		"content": "package main\n",
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if !strings.Contains(result.DisplayText, "no changes to main.go") {
		t.Fatalf("expected no-op display text, got %q", result.DisplayText)
	}
	if toolMetaBool(result.Meta, "changed_workspace") || toolMetaBool(result.Meta, "requires_verification") {
		t.Fatalf("no-op write must not report workspace changes, got %#v", result.Meta)
	}
	if paths := toolMetaStringSlice(result.Meta, "changed_paths"); len(paths) != 0 {
		t.Fatalf("no-op write must not report changed paths, got %#v", paths)
	}
	if got := toolMetaInt(result.Meta, "changed_count"); got != 0 {
		t.Fatalf("expected changed_count=0, got %#v", result.Meta)
	}
	if got := toolMetaInt(result.Meta, "bytes_written"); got != 0 {
		t.Fatalf("expected bytes_written=0 for no-op, got %#v", result.Meta)
	}
	if diff := toolMetaString(result.Meta, "unified_diff"); diff != "" {
		t.Fatalf("no-op write must not report a unified diff, got %q", diff)
	}
}

func TestWriteFileExecuteDetailedReportsCommittedMutationWhenPostEditHookFails(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package old\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewWriteFileTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			return true, nil
		},
		RunHook: func(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
			if event == HookPostEdit {
				return HookVerdict{}, fmt.Errorf("post edit hook failed")
			}
			return HookVerdict{Allow: true}, nil
		},
	})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"path":    "main.go",
		"content": "package main\n",
	})
	if err == nil {
		t.Fatalf("expected post edit hook failure")
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || string(data) != "package main\n" {
		t.Fatalf("expected write to be committed before hook failure, data=%q err=%v", string(data), readErr)
	}
	if !toolMetaBool(result.Meta, "changed_workspace") || !toolMetaBool(result.Meta, "requires_verification") {
		t.Fatalf("committed failed write must require verification, got %#v", result.Meta)
	}
	if unifiedDiff := toolMetaString(result.Meta, "unified_diff"); !strings.Contains(unifiedDiff, "+package main") {
		t.Fatalf("expected committed failed write diff, got %q", unifiedDiff)
	}
}

func TestReplaceInFileExecuteDetailedIncludesUnifiedDiff(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc oldName() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewReplaceInFileTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			return true, nil
		},
	})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"path":    "main.go",
		"search":  "oldName",
		"replace": "newName",
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if got := toolMetaString(result.Meta, "path"); got != "main.go" {
		t.Fatalf("expected display path metadata, got %#v", result.Meta)
	}
	if toolMetaInt(result.Meta, "applied_replacements") != 1 {
		t.Fatalf("expected replacement count metadata, got %#v", result.Meta)
	}
	unifiedDiff := toolMetaString(result.Meta, "unified_diff")
	for _, want := range []string{
		"diff --git a/main.go b/main.go",
		"-func oldName() {}",
		"+func newName() {}",
	} {
		if !strings.Contains(unifiedDiff, want) {
			t.Fatalf("expected unified diff to contain %q, got %q", want, unifiedDiff)
		}
	}
}

func TestReplaceInFileExecuteDetailedReportsNoWorkspaceChangeForNoOp(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc oldName() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewReplaceInFileTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			t.Fatalf("no-op replace_in_file must not request edit preview: %#v", preview)
			return false, nil
		},
	})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"path":    "main.go",
		"search":  "oldName",
		"replace": "oldName",
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if !strings.Contains(result.DisplayText, "no changes to main.go") {
		t.Fatalf("expected no-op display text, got %q", result.DisplayText)
	}
	if toolMetaBool(result.Meta, "changed_workspace") || toolMetaBool(result.Meta, "requires_verification") {
		t.Fatalf("no-op replacement must not report workspace changes, got %#v", result.Meta)
	}
	if paths := toolMetaStringSlice(result.Meta, "changed_paths"); len(paths) != 0 {
		t.Fatalf("no-op replacement must not report changed paths, got %#v", paths)
	}
	if got := toolMetaInt(result.Meta, "changed_count"); got != 0 {
		t.Fatalf("expected changed_count=0, got %#v", result.Meta)
	}
	if got := toolMetaInt(result.Meta, "applied_replacements"); got != 0 {
		t.Fatalf("expected applied_replacements=0 for no-op, got %#v", result.Meta)
	}
	if diff := toolMetaString(result.Meta, "unified_diff"); diff != "" {
		t.Fatalf("no-op replacement must not report a unified diff, got %q", diff)
	}
}

func TestReplaceInFileExecuteDetailedReportsCommittedMutationWhenPostEditHookFails(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc oldName() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewReplaceInFileTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			return true, nil
		},
		RunHook: func(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
			if event == HookPostEdit {
				return HookVerdict{}, fmt.Errorf("post edit hook failed")
			}
			return HookVerdict{Allow: true}, nil
		},
	})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"path":    "main.go",
		"search":  "oldName",
		"replace": "newName",
	})
	if err == nil {
		t.Fatalf("expected post edit hook failure")
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || !strings.Contains(string(data), "newName") {
		t.Fatalf("expected replacement to be committed before hook failure, data=%q err=%v", string(data), readErr)
	}
	if !toolMetaBool(result.Meta, "changed_workspace") || !toolMetaBool(result.Meta, "requires_verification") {
		t.Fatalf("committed failed replacement must require verification, got %#v", result.Meta)
	}
	if got := toolMetaInt(result.Meta, "applied_replacements"); got != 1 {
		t.Fatalf("expected replacement count to survive post-hook failure, got %#v", result.Meta)
	}
	if unifiedDiff := toolMetaString(result.Meta, "unified_diff"); !strings.Contains(unifiedDiff, "+func newName() {}") {
		t.Fatalf("expected committed failed replacement diff, got %q", unifiedDiff)
	}
}

func TestApplyEditProposalExecuteDetailedIncludesUnifiedDiff(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc oldName() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyEditProposalTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			return true, nil
		},
	})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"file":         "main.go",
		"operation":    "replace_in_file",
		"exact_search": "oldName",
		"replacement":  "newName",
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if got := toolMetaString(result.Meta, "path"); got != "main.go" {
		t.Fatalf("expected display path metadata, got %#v", result.Meta)
	}
	unifiedDiff := toolMetaString(result.Meta, "unified_diff")
	for _, want := range []string{
		"diff --git a/main.go b/main.go",
		"-func oldName() {}",
		"+func newName() {}",
	} {
		if !strings.Contains(unifiedDiff, want) {
			t.Fatalf("expected unified diff to contain %q, got %q", want, unifiedDiff)
		}
	}
}

func TestApplyEditProposalReportsCommittedMutationWhenPostEditHookFails(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc oldName() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyEditProposalTool(Workspace{
		BaseRoot: root,
		Root:     root,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			return true, nil
		},
		RunHook: func(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
			if event == HookPostEdit {
				return HookVerdict{}, fmt.Errorf("post edit hook failed")
			}
			return HookVerdict{Allow: true}, nil
		},
	})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"file":         "main.go",
		"operation":    "replace_in_file",
		"exact_search": "oldName",
		"replacement":  "newName",
	})
	if err == nil {
		t.Fatalf("expected post edit hook failure")
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || !strings.Contains(string(data), "newName") {
		t.Fatalf("expected proposal edit to be committed before hook failure, data=%q err=%v", string(data), readErr)
	}
	if !toolMetaBool(result.Meta, "changed_workspace") || !toolMetaBool(result.Meta, "requires_verification") {
		t.Fatalf("committed failed proposal must require verification, got %#v", result.Meta)
	}
	if unifiedDiff := toolMetaString(result.Meta, "unified_diff"); !strings.Contains(unifiedDiff, "+func newName() {}") {
		t.Fatalf("expected committed failed proposal diff, got %q", unifiedDiff)
	}
}

func TestApplyPatchExecuteDetailedIncludesEffectiveWorkspaceRoots(t *testing.T) {
	baseRoot := t.TempDir()
	activeRoot := filepath.Join(baseRoot, "worktree")
	if err := os.MkdirAll(activeRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(activeRoot, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: baseRoot,
		Root:     activeRoot,
		PreviewEdit: func(preview EditPreview) (bool, error) {
			return true, nil
		},
	})

	result, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"owner_node_id": "worker-a",
		"patch": strings.Join([]string{
			"*** Begin Patch",
			"*** Update File: main.go",
			"@@",
			" package main",
			"+func main() {}",
			"*** End Patch",
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if got := toolMetaString(result.Meta, "owner_node_id"); got != "worker-a" {
		t.Fatalf("expected owner_node_id metadata, got %#v", result.Meta)
	}
	if got := toolMetaString(result.Meta, "workspace_root"); !sameFilePath(got, baseRoot) {
		t.Fatalf("expected base workspace_root %q, got %#v", baseRoot, result.Meta)
	}
	if got := toolMetaString(result.Meta, "active_workspace_root"); !sameFilePath(got, activeRoot) {
		t.Fatalf("expected active workspace root %q, got %#v", activeRoot, result.Meta)
	}
	roots := toolMetaStringSlice(result.Meta, "workspace_roots")
	if len(roots) != 2 || !sameFilePath(roots[0], baseRoot) || !sameFilePath(roots[1], activeRoot) {
		t.Fatalf("expected effective workspace_roots [%q %q], got %#v", baseRoot, activeRoot, result.Meta)
	}
}

func TestApplyPatchHookPayloadIncludesEffectiveWorkspaceRoots(t *testing.T) {
	baseRoot := t.TempDir()
	activeRoot := filepath.Join(baseRoot, "worktree")
	if err := os.MkdirAll(activeRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(activeRoot, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	var prePayload HookPayload
	tool := NewApplyPatchTool(Workspace{
		BaseRoot: baseRoot,
		Root:     activeRoot,
		RunHook: func(ctx context.Context, event HookEvent, payload HookPayload) (HookVerdict, error) {
			_ = ctx
			if event == HookPreToolUse {
				prePayload = payload
			}
			return HookVerdict{Allow: true}, nil
		},
		PreviewEdit: func(preview EditPreview) (bool, error) {
			return true, nil
		},
	})

	_, err := tool.ExecuteDetailed(context.Background(), map[string]any{
		"patch": strings.Join([]string{
			"*** Begin Patch",
			"*** Update File: main.go",
			"@@",
			" package main",
			"+func main() {}",
			"*** End Patch",
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if got := toolMetaString(prePayload, "workspace_root"); !sameFilePath(got, baseRoot) {
		t.Fatalf("expected hook base workspace_root %q, got %#v", baseRoot, prePayload)
	}
	if got := toolMetaString(prePayload, "active_workspace_root"); !sameFilePath(got, activeRoot) {
		t.Fatalf("expected hook active workspace root %q, got %#v", activeRoot, prePayload)
	}
	if got := toolMetaString(prePayload, "work_dir"); !sameFilePath(got, activeRoot) {
		t.Fatalf("expected hook work_dir %q, got %#v", activeRoot, prePayload)
	}
	roots := toolMetaStringSlice(prePayload, "workspace_roots")
	if len(roots) != 2 || !sameFilePath(roots[0], baseRoot) || !sameFilePath(roots[1], activeRoot) {
		t.Fatalf("expected hook workspace_roots [%q %q], got %#v", baseRoot, activeRoot, prePayload)
	}
}

func TestGitAddExecuteDetailedReturnsStructuredMeta(t *testing.T) {
	repo := initTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "feature.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	registry := NewToolRegistry(NewGitAddTool(Workspace{BaseRoot: repo, Root: repo}))
	payload, err := json.Marshal(map[string]any{
		"paths": []string{"feature.txt"},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	result, err := registry.ExecuteDetailed(context.Background(), "git_add", string(payload))
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if toolMetaString(result.Meta, "effect") != "git_mutation" {
		t.Fatalf("expected git_mutation effect, got %#v", result.Meta)
	}
	if !toolMetaBool(result.Meta, "staged") || toolMetaInt(result.Meta, "staged_count") != 1 {
		t.Fatalf("expected staged metadata, got %#v", result.Meta)
	}
	paths := toolMetaStringSlice(result.Meta, "paths")
	if len(paths) != 1 || paths[0] != "feature.txt" {
		t.Fatalf("expected staged paths metadata, got %#v", result.Meta)
	}
}

func TestGitCommitExecuteDetailedReturnsStructuredMeta(t *testing.T) {
	repo := initTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "feature.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ws := Workspace{BaseRoot: repo, Root: repo}
	if _, err := NewGitAddTool(ws).Execute(context.Background(), map[string]any{
		"paths": []any{"feature.txt"},
	}); err != nil {
		t.Fatalf("git_add: %v", err)
	}
	registry := NewToolRegistry(NewGitCommitTool(ws))
	payload, err := json.Marshal(map[string]any{
		"message": "Add feature file",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	result, err := registry.ExecuteDetailed(context.Background(), "git_commit", string(payload))
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if !toolMetaBool(result.Meta, "created_commit") {
		t.Fatalf("expected created_commit metadata, got %#v", result.Meta)
	}
	if toolMetaString(result.Meta, "commit_subject") != "Add feature file" {
		t.Fatalf("expected commit subject metadata, got %#v", result.Meta)
	}
	if toolMetaString(result.Meta, "commit_sha") == "" {
		t.Fatalf("expected commit sha metadata, got %#v", result.Meta)
	}
}

func TestGitPushExecuteDetailedReturnsStructuredMeta(t *testing.T) {
	repo := initTestGitRepo(t)
	remote := initBareRemote(t)
	mustRunGit(t, repo, "remote", "add", "origin", remote)
	mustRunGit(t, repo, "checkout", "-b", "feature/push-meta")
	if err := os.WriteFile(filepath.Join(repo, "push.txt"), []byte("push\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ws := Workspace{BaseRoot: repo, Root: repo}
	if _, err := NewGitAddTool(ws).Execute(context.Background(), map[string]any{
		"paths": []any{"push.txt"},
	}); err != nil {
		t.Fatalf("git_add: %v", err)
	}
	if _, err := NewGitCommitTool(ws).Execute(context.Background(), map[string]any{
		"message": "Add push meta file",
	}); err != nil {
		t.Fatalf("git_commit: %v", err)
	}
	registry := NewToolRegistry(NewGitPushTool(ws))
	payload, err := json.Marshal(map[string]any{
		"remote": "origin",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	result, err := registry.ExecuteDetailed(context.Background(), "git_push", string(payload))
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if !toolMetaBool(result.Meta, "pushed") {
		t.Fatalf("expected pushed metadata, got %#v", result.Meta)
	}
	if toolMetaString(result.Meta, "remote") != "origin" || toolMetaString(result.Meta, "branch") != "feature/push-meta" {
		t.Fatalf("expected remote/branch metadata, got %#v", result.Meta)
	}
}

func TestGitCreatePRExecuteDetailedReturnsStructuredMeta(t *testing.T) {
	repo := initTestGitRepo(t)
	remote := initBareRemote(t)
	mustRunGit(t, repo, "remote", "add", "origin", remote)
	mustRunGit(t, repo, "checkout", "-b", "feature/pr-meta")
	if err := os.WriteFile(filepath.Join(repo, "pr.txt"), []byte("pr\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ws := Workspace{BaseRoot: repo, Root: repo}
	if _, err := NewGitAddTool(ws).Execute(context.Background(), map[string]any{
		"paths": []any{"pr.txt"},
	}); err != nil {
		t.Fatalf("git_add: %v", err)
	}
	if _, err := NewGitCommitTool(ws).Execute(context.Background(), map[string]any{
		"message": "Add pr meta file",
	}); err != nil {
		t.Fatalf("git_commit: %v", err)
	}

	capturePath := filepath.Join(t.TempDir(), "gh_args.txt")
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	installFakeGh(t, binDir)
	t.Setenv("GH_ARGS_FILE", capturePath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	registry := NewToolRegistry(NewGitCreatePRTool(ws))
	payload, err := json.Marshal(map[string]any{
		"title":       "Feature PR",
		"body":        "Meta coverage.",
		"base_branch": "main",
		"draft":       true,
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	result, err := registry.ExecuteDetailed(context.Background(), "git_create_pr", string(payload))
	if err != nil {
		t.Fatalf("ExecuteDetailed: %v", err)
	}
	if !toolMetaBool(result.Meta, "pr_created") {
		t.Fatalf("expected pr_created metadata, got %#v", result.Meta)
	}
	if toolMetaString(result.Meta, "pr_url") != "https://github.com/example/repo/pull/123" {
		t.Fatalf("expected PR URL metadata, got %#v", result.Meta)
	}
	if toolMetaString(result.Meta, "branch") != "feature/pr-meta" || !toolMetaBool(result.Meta, "draft") {
		t.Fatalf("expected branch/draft metadata, got %#v", result.Meta)
	}
}
