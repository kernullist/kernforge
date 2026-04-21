package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	parallelEditableWorkerCooldown = 8 * time.Second
	parallelEditableWorkerMaxTurns = 4
)

var parallelEditableWorkerAllowedTools = map[string]struct{}{
	"read_file":   {},
	"list_files":  {},
	"grep":        {},
	"apply_patch": {},
}

type parallelEditableWorkerPlan struct {
	Node       TaskNode
	Assignment SpecialistAssignment
	LeasePaths []string
	Reason     string
	RouteHint  string
	Trigger    string
}

type deferredParallelEditableWorkerPlan struct {
	Plan   parallelEditableWorkerPlan
	Reason string
}

type parallelEditableWorkerResult struct {
	Plan          parallelEditableWorkerPlan
	Summary       string
	Detail        string
	ApplyCall     ToolCall
	ApplyResult   ToolExecutionResult
	Applied       bool
	Skipped       bool
	Err           error
	ErrorToolName string
}

func (a *Agent) maybeRunInteractiveParallelEditableWorkers(ctx context.Context, trigger string) error {
	if a == nil || a.Session == nil || a.Tools == nil || a.Session.TaskState == nil {
		return nil
	}
	if len(a.Session.TaskState.ExecutorParallelNodes) == 0 {
		return nil
	}
	graph := a.Session.TaskGraph
	if graph == nil || len(graph.Nodes) == 0 {
		return nil
	}
	candidates := a.executorAwareEditableWorkerCandidates(2)
	if len(candidates) == 0 {
		return nil
	}

	updated := false
	plans := make([]parallelEditableWorkerPlan, 0, len(candidates))
	for _, node := range candidates {
		assignment, ok := selectEditableSpecialistForTaskNode(a.Config, node, a.Session.TaskState, trigger)
		if !ok {
			continue
		}
		ownership := specialistOwnershipPaths(assignment.Profile, node.EditableOwnershipPaths)
		a.Session.RecordTaskGraphEditableAssignment(node.ID, assignment.Profile.Name, assignment.Reason, ownership, node.EditableWorktreeRoot, node.EditableWorktreeBranch)
		updated = true
		updatedNode, ok := a.Session.TaskGraph.Node(node.ID)
		if ok {
			node = updatedNode
		}
		plan, ok := buildParallelEditableWorkerPlan(node, assignment, trigger)
		if !ok {
			continue
		}
		if len(node.EditableLeasePaths) == 0 && len(plan.LeasePaths) > 0 {
			a.Session.RecordTaskGraphEditableLease(node.ID, plan.LeasePaths, firstNonBlankString(node.EditableLeaseReason, "parallel-edit-worker"))
			updated = true
			if updatedNode, ok := a.Session.TaskGraph.Node(node.ID); ok {
				plan.Node = updatedNode
			}
		}
		plans = append(plans, plan)
	}
	if len(plans) == 0 {
		if updated && a.Store != nil {
			return a.Store.Save(a.Session)
		}
		return nil
	}
	plans, deferred := a.revalidateParallelEditableWorkerPlans(plans)

	state := a.Session.EnsureTaskState()
	if len(deferred) > 0 {
		deferredIDs := make([]string, 0, len(deferred))
		for _, item := range deferred {
			deferredIDs = append(deferredIDs, item.Plan.Node.ID)
			a.Session.SetPlanNodeLifecycle(item.Plan.Node.ID, "pending", item.Reason)
			a.Session.RecordTaskGraphEditableWorkerEvidence(item.Plan.Node.ID, item.Reason)
			state.RecordEvent("parallel_edit_worker_deferred", item.Plan.Node.ID, "editable_worker", item.Reason, strings.Join(item.Plan.LeasePaths, ", "), "deferred", false)
			updated = true
		}
		filteredNodeIDs := filterParallelWorkerNodeIDs(state.ExecutorParallelNodes, deferredIDs)
		filteredGuidance := filterParallelWorkerGuidance(state.ExecutorParallelGuidance, deferredIDs)
		if state.SetExecutorParallelAssignments(filteredNodeIDs, filteredGuidance) {
			updated = true
		}
	}
	if len(plans) == 0 {
		if updated && a.Store != nil {
			return a.Store.Save(a.Session)
		}
		return nil
	}
	for _, plan := range plans {
		state.RecordEvent("parallel_edit_worker_start", plan.Node.ID, "editable_worker", "Parallel editable worker started for "+plan.Node.Title, plan.Reason, "started", false)
		updated = true
	}

	results := make(chan parallelEditableWorkerResult, len(plans))
	var wg sync.WaitGroup
	for _, plan := range plans {
		plan := plan
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- a.runParallelEditableWorker(ctx, plan)
		}()
	}
	wg.Wait()
	close(results)

	for item := range results {
		if item.Skipped {
			continue
		}
		summary := strings.TrimSpace(item.Summary)
		detail := compactPromptSection(strings.TrimSpace(item.Detail), 220)
		if item.Err != nil {
			if summary == "" {
				summary = "Parallel editable worker could not finish " + item.Plan.Node.Title
			}
			if detail == "" {
				detail = compactPromptSection(item.Err.Error(), 220)
			}
			a.Session.RecordTaskGraphEditableWorkerEvidence(item.Plan.Node.ID, summary)
			state.RecordEvent("parallel_edit_worker_error", item.Plan.Node.ID, firstNonBlankString(item.ErrorToolName, "editable_worker"), summary, detail, "error", false)
			updated = true
			continue
		}
		if item.Applied {
			if item.ApplyResult.Meta == nil {
				item.ApplyResult.Meta = map[string]any{}
			}
			item.ApplyResult.Meta["parallel_worker"] = true
			item.ApplyResult.Meta["parallel_edit_worker"] = true
			a.noteToolExecutionResultDetailed(item.ApplyCall, item.ApplyResult, nil)
			if verificationSummary := a.maybeStartParallelEditableVerification(ctx, item.Plan, item.ApplyResult); verificationSummary != "" {
				if summary != "" {
					summary += " " + verificationSummary
				} else {
					summary = verificationSummary
				}
			}
			updated = true
		}
		if summary == "" {
			if item.Applied {
				summary = summarizeParallelEditableWorkerApplyResult(item.ApplyResult)
			} else {
				summary = "Parallel editable worker inspected the leased edit scope without applying a patch."
			}
		}
		if detail == "" {
			detail = summary
		}
		a.Session.RecordTaskGraphEditableWorkerEvidence(item.Plan.Node.ID, summary)
		eventKind := "parallel_edit_worker_result"
		if !item.Applied {
			eventKind = "parallel_edit_worker_noop"
		}
		state.RecordEvent(eventKind, item.Plan.Node.ID, firstNonBlankString(item.ApplyCall.Name, "editable_worker"), summary, detail, "ok", false)
		updated = true
	}
	if updated && a.Store != nil {
		return a.Store.Save(a.Session)
	}
	return nil
}

func (a *Agent) revalidateParallelEditableWorkerPlans(plans []parallelEditableWorkerPlan) ([]parallelEditableWorkerPlan, []deferredParallelEditableWorkerPlan) {
	if a == nil || a.Session == nil || a.Session.TaskGraph == nil {
		return plans, nil
	}
	type claimedLease struct {
		NodeID string
		Lease  []string
	}
	claimed := make([]claimedLease, 0, len(plans)+1)
	if state := a.Session.TaskState; state != nil {
		if focusNodeID := strings.TrimSpace(state.ExecutorFocusNode); focusNodeID != "" {
			if focusNode, ok := a.Session.TaskGraph.Node(focusNodeID); ok {
				if focusLease := bestEffortParallelEditableLeasePaths(focusNode); len(focusLease) > 0 {
					claimed = append(claimed, claimedLease{
						NodeID: focusNodeID,
						Lease:  focusLease,
					})
				}
			}
		}
	}
	safe := make([]parallelEditableWorkerPlan, 0, len(plans))
	deferred := make([]deferredParallelEditableWorkerPlan, 0)
	for _, plan := range plans {
		leasePaths := normalizeTaskStateList(plan.LeasePaths, 32)
		if len(leasePaths) == 0 {
			leasePaths = bestEffortParallelEditableLeasePaths(plan.Node)
		}
		conflictNodeID := ""
		for _, existing := range claimed {
			if editableLeaseCollectionsOverlap(leasePaths, existing.Lease) {
				conflictNodeID = existing.NodeID
				break
			}
		}
		if conflictNodeID != "" {
			deferred = append(deferred, deferredParallelEditableWorkerPlan{
				Plan:   plan,
				Reason: fmt.Sprintf("Deferred secondary edit lane because its lease overlaps with %s after refresh.", conflictNodeID),
			})
			continue
		}
		safe = append(safe, plan)
		if len(leasePaths) > 0 {
			claimed = append(claimed, claimedLease{
				NodeID: plan.Node.ID,
				Lease:  leasePaths,
			})
		}
	}
	return safe, deferred
}

func (a *Agent) executorAwareEditableWorkerCandidates(limit int) []TaskNode {
	if a == nil || a.Session == nil || a.Session.TaskGraph == nil || a.Session.TaskState == nil {
		return nil
	}
	candidates := make([]TaskNode, 0, limit)
	seen := map[string]struct{}{}
	for _, nodeID := range a.Session.TaskState.ExecutorParallelNodes {
		node, ok := a.Session.TaskGraph.Node(nodeID)
		if !ok || !taskNodeEligibleForParallelEditableWorker(node) {
			continue
		}
		if _, exists := seen[node.ID]; exists {
			continue
		}
		seen[node.ID] = struct{}{}
		candidates = append(candidates, node)
		if limit > 0 && len(candidates) >= limit {
			break
		}
	}
	return candidates
}

func taskNodeEligibleForParallelEditableWorker(node TaskNode) bool {
	if !isPrimaryTaskNode(node) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(node.Kind), "edit") {
		return false
	}
	status := canonicalTaskNodeStatus(node.Status)
	if status == "completed" || status == "failed" || status == "stale" || status == "superseded" || status == "canceled" || status == "preempted" {
		return false
	}
	if !node.EditableWorkerAt.IsZero() && time.Since(node.EditableWorkerAt) < parallelEditableWorkerCooldown {
		return false
	}
	return len(bestEffortParallelEditableLeasePaths(node)) > 0
}

func buildParallelEditableWorkerPlan(node TaskNode, assignment SpecialistAssignment, trigger string) (parallelEditableWorkerPlan, bool) {
	leasePaths := bestEffortParallelEditableLeasePaths(node)
	if len(leasePaths) == 0 {
		return parallelEditableWorkerPlan{}, false
	}
	return parallelEditableWorkerPlan{
		Node:       node,
		Assignment: assignment,
		LeasePaths: leasePaths,
		Reason:     "Parallel editable worker can safely own this disjoint edit lane.",
		RouteHint:  firstNonBlankString(assignment.Reason, "parallel-edit-worker"),
		Trigger:    strings.TrimSpace(trigger),
	}, true
}

func (a *Agent) runParallelEditableWorker(ctx context.Context, plan parallelEditableWorkerPlan) parallelEditableWorkerResult {
	client, model := a.specialistClient(plan.Assignment.Profile)
	if client == nil || strings.TrimSpace(model) == "" {
		return parallelEditableWorkerResult{Plan: plan, Skipped: true}
	}
	tools := parallelEditableWorkerToolDefinitions(a.Tools)
	if len(tools) == 0 {
		return parallelEditableWorkerResult{Plan: plan, Skipped: true}
	}

	messages := []Message{{
		Role: "user",
		Text: buildSpecialistEditableWorkerPrompt(a.Session.TaskState, plan),
	}}
	lastText := ""
	lastErr := error(nil)
	lastErrToolName := ""

	for turn := 0; turn < parallelEditableWorkerMaxTurns; turn++ {
		resp, err := a.completeModelTurnWithClient(ctx, client, ChatRequest{
			Model:       model,
			System:      buildSpecialistEditableWorkerSystemPrompt(plan.Assignment.Profile),
			Messages:    messages,
			Tools:       tools,
			MaxTokens:   min(768, max(256, a.Config.MaxTokens/3)),
			Temperature: 0.1,
			WorkingDir:  a.Session.WorkingDir,
		})
		if err != nil {
			return parallelEditableWorkerResult{
				Plan:    plan,
				Err:     err,
				Summary: "Parallel editable worker model request failed for " + plan.Node.Title,
				Detail:  err.Error(),
			}
		}
		resp.Message.Text = sanitizeAssistantMessageText(resp.Message.Text, len(resp.Message.ToolCalls) > 0)
		lastText = strings.TrimSpace(resp.Message.Text)
		if len(resp.Message.ToolCalls) == 0 {
			if patch := extractPatchDocument(lastText); patch != "" {
				call, err := buildParallelEditableWorkerPatchCall(plan.Node.ID, patch)
				if err != nil {
					return parallelEditableWorkerResult{
						Plan:    plan,
						Err:     err,
						Summary: "Parallel editable worker produced an invalid patch payload for " + plan.Node.Title,
						Detail:  err.Error(),
					}
				}
				result, execErr := a.Tools.ExecuteDetailed(ctx, call.Name, call.Arguments)
				if execErr != nil {
					return parallelEditableWorkerResult{
						Plan:          plan,
						Err:           execErr,
						Summary:       "Parallel editable worker patch application failed for " + plan.Node.Title,
						Detail:        firstNonBlankString(result.DisplayText, execErr.Error()),
						ApplyCall:     call,
						ApplyResult:   result,
						ErrorToolName: call.Name,
					}
				}
				return parallelEditableWorkerResult{
					Plan:        plan,
					Summary:     summarizeParallelEditableWorkerApplyResult(result),
					Detail:      firstNonBlankString(stripPatchFromText(lastText), result.DisplayText),
					ApplyCall:   call,
					ApplyResult: result,
					Applied:     true,
				}
			}
			if lastText != "" {
				return parallelEditableWorkerResult{
					Plan:    plan,
					Summary: summarizeParallelEditableWorkerNoop(lastText),
					Detail:  lastText,
				}
			}
			continue
		}

		messages = append(messages, resp.Message)
		executedTool := false
		for _, rawCall := range resp.Message.ToolCalls {
			call := withParallelEditableWorkerOwnerNodeID(rawCall, plan.Node.ID)
			if !parallelEditableWorkerAllowsTool(call.Name) {
				msg := "ERROR: only read_file, list_files, grep, and apply_patch are allowed for this worker."
				messages = append(messages, Message{
					Role:       "tool",
					ToolCallID: call.ID,
					ToolName:   call.Name,
					Text:       msg,
					IsError:    true,
				})
				lastErr = fmt.Errorf("disallowed tool for parallel editable worker: %s", strings.TrimSpace(call.Name))
				lastErrToolName = ""
				continue
			}
			result, execErr := a.Tools.ExecuteDetailed(ctx, call.Name, call.Arguments)
			display := strings.TrimSpace(result.DisplayText)
			if execErr != nil {
				if display == "" {
					display = execErr.Error()
				} else {
					display += "\n\nERROR: " + execErr.Error()
				}
			}
			messages = append(messages, Message{
				Role:       "tool",
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Text:       display,
				ToolMeta:   result.Meta,
				IsError:    execErr != nil,
			})
			executedTool = true
			if execErr != nil {
				lastErr = execErr
				lastErrToolName = strings.TrimSpace(call.Name)
				continue
			}
			if strings.TrimSpace(call.Name) == "apply_patch" {
				return parallelEditableWorkerResult{
					Plan:        plan,
					Summary:     summarizeParallelEditableWorkerApplyResult(result),
					Detail:      firstNonBlankString(lastText, result.DisplayText),
					ApplyCall:   call,
					ApplyResult: result,
					Applied:     true,
				}
			}
		}
		if !executedTool && lastText == "" {
			break
		}
	}

	if lastErr != nil {
		return parallelEditableWorkerResult{
			Plan:          plan,
			Err:           lastErr,
			Summary:       "Parallel editable worker stopped before patching " + plan.Node.Title,
			Detail:        firstNonBlankString(lastText, lastErr.Error()),
			ErrorToolName: lastErrToolName,
		}
	}
	if lastText != "" {
		return parallelEditableWorkerResult{
			Plan:    plan,
			Summary: summarizeParallelEditableWorkerNoop(lastText),
			Detail:  lastText,
		}
	}
	return parallelEditableWorkerResult{
		Plan:    plan,
		Summary: "Parallel editable worker inspected the leased edit scope without applying a patch.",
	}
}

func (a *Agent) maybeStartParallelEditableVerification(ctx context.Context, plan parallelEditableWorkerPlan, applyResult ToolExecutionResult) string {
	if a == nil || a.Tools == nil || !configAutoVerify(a.Config) {
		return ""
	}
	changed := normalizeTaskStateList(toolMetaStringSlice(applyResult.Meta, "changed_paths"), 8)
	if len(changed) == 0 {
		return ""
	}
	verifyRoot := parallelEditableVerificationRoot(a, plan.Node)
	tuning := VerificationTuning{}
	if a.VerifyHistory != nil {
		if loaded, err := a.VerifyHistory.PlannerTuning(verifyRoot); err == nil {
			tuning = loaded
		}
	}
	planSpec := buildVerificationPlanWithTuning(verifyRoot, changed, VerificationAdaptive, tuning)
	commands := backgroundVerificationCommandsFromPlan(planSpec, 4)
	if len(commands) == 0 {
		return ""
	}
	payload, err := json.Marshal(map[string]any{
		"commands":          commands,
		"owner_node_id":     plan.Node.ID,
		"verification_like": true,
	})
	if err != nil {
		return ""
	}
	call := ToolCall{
		Name:      "run_shell_bundle_background",
		Arguments: string(payload),
	}
	result, execErr := a.Tools.ExecuteDetailed(ctx, call.Name, call.Arguments)
	if execErr != nil {
		if parallelEditableVerificationStartOptionalError(execErr) {
			return ""
		}
		detail := firstNonBlankString(strings.TrimSpace(result.DisplayText), execErr.Error())
		a.Session.EnsureTaskState().RecordEvent("parallel_edit_verification_error", plan.Node.ID, call.Name, "Parallel editable verification could not start.", detail, "error", false)
		return ""
	}
	if result.Meta == nil {
		result.Meta = map[string]any{}
	}
	result.Meta["parallel_worker"] = true
	result.Meta["parallel_edit_worker"] = true
	a.noteToolExecutionResultDetailed(call, result, nil)
	bundleID := strings.TrimSpace(toolMetaString(result.Meta, "bundle_id"))
	if bundleID == "" {
		return "Started specialist-aware background verification."
	}
	return "Started specialist-aware background verification bundle " + bundleID + "."
}

func buildSpecialistEditableWorkerSystemPrompt(profile SpecialistSubagentProfile) string {
	lines := []string{
		"You are a specialist editable worker assisting a terminal coding agent.",
		"Own exactly one secondary edit node and stay strictly inside the leased ownership boundary.",
		"Use only read_file, list_files, grep, and apply_patch.",
		"Inspect before editing when needed, keep the patch minimal, and stop after one successful patch.",
		"If no safe change is justified, return NO_CHANGE followed by one short reason.",
	}
	if strings.TrimSpace(profile.Name) != "" {
		lines = append(lines, "Specialist role: "+strings.TrimSpace(profile.Name))
	}
	if strings.TrimSpace(profile.Description) != "" {
		lines = append(lines, strings.TrimSpace(profile.Description))
	}
	if strings.TrimSpace(profile.Prompt) != "" {
		lines = append(lines, strings.TrimSpace(profile.Prompt))
	}
	return strings.Join(lines, "\n")
}

func buildSpecialistEditableWorkerPrompt(state *TaskState, plan parallelEditableWorkerPlan) string {
	var b strings.Builder
	if state != nil {
		if strings.TrimSpace(state.Goal) != "" {
			b.WriteString("Goal:\n")
			b.WriteString(strings.TrimSpace(state.Goal))
			b.WriteString("\n\n")
		}
		if strings.TrimSpace(state.PlanSummary) != "" {
			b.WriteString("Plan summary:\n")
			b.WriteString(compactPromptSection(compactPlanSummary(state.PlanSummary), 500))
			b.WriteString("\n\n")
		}
	}
	if strings.TrimSpace(plan.Trigger) != "" {
		b.WriteString("Trigger:\n")
		b.WriteString(strings.TrimSpace(plan.Trigger))
		b.WriteString("\n\n")
	}
	b.WriteString("Task node:\n")
	b.WriteString(fmt.Sprintf("- ID: %s\n- Title: %s\n- Kind: %s\n- Status: %s\n", plan.Node.ID, plan.Node.Title, plan.Node.Kind, plan.Node.Status))
	if strings.TrimSpace(plan.RouteHint) != "" {
		b.WriteString("- Routing reason: " + strings.TrimSpace(plan.RouteHint) + "\n")
	}
	if len(plan.LeasePaths) > 0 {
		b.WriteString("- Lease paths: " + strings.Join(plan.LeasePaths, ", ") + "\n")
	}
	if concrete := firstConcreteEditableLeasePath(plan.LeasePaths); concrete != "" {
		b.WriteString("- Preferred starting file: " + concrete + "\n")
	} else if baseDir := firstEditableLeaseBaseDir(plan.LeasePaths); baseDir != "" {
		b.WriteString("- Preferred lease root: " + baseDir + "\n")
	}
	if strings.TrimSpace(plan.Node.MicroWorkerBrief) != "" {
		b.WriteString("- Micro-worker brief: " + compactPromptSection(plan.Node.MicroWorkerBrief, 180) + "\n")
	}
	if strings.TrimSpace(plan.Node.ReadOnlyWorkerSummary) != "" {
		b.WriteString("- Read-only worker evidence: " + compactPromptSection(plan.Node.ReadOnlyWorkerSummary, 180) + "\n")
	}
	if strings.TrimSpace(plan.Node.LifecycleNote) != "" {
		b.WriteString("- Lifecycle note: " + compactPromptSection(plan.Node.LifecycleNote, 180) + "\n")
	}
	b.WriteString("\nExecution rules:\n")
	b.WriteString("1. owner_node_id routing is handled automatically for each tool call.\n")
	b.WriteString("2. Stay inside the leased scope only.\n")
	b.WriteString("3. Use apply_patch for the final change.\n")
	b.WriteString("4. After the patch, stop. If no safe patch is needed, return NO_CHANGE with one short reason.\n")
	return b.String()
}

func parallelEditableWorkerToolDefinitions(registry *ToolRegistry) []ToolDefinition {
	if registry == nil {
		return nil
	}
	defs := registry.Definitions()
	out := make([]ToolDefinition, 0, len(parallelEditableWorkerAllowedTools))
	for _, def := range defs {
		if !parallelEditableWorkerAllowsTool(def.Name) {
			continue
		}
		out = append(out, def)
	}
	return out
}

func parallelEditableWorkerAllowsTool(name string) bool {
	_, ok := parallelEditableWorkerAllowedTools[strings.TrimSpace(name)]
	return ok
}

func withParallelEditableWorkerOwnerNodeID(call ToolCall, ownerNodeID string) ToolCall {
	ownerNodeID = strings.TrimSpace(ownerNodeID)
	if ownerNodeID == "" {
		return call
	}
	if !parallelEditableWorkerAllowsTool(call.Name) {
		return call
	}
	args := map[string]any{}
	if strings.TrimSpace(call.Arguments) != "" {
		if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
			return call
		}
	}
	args["owner_node_id"] = ownerNodeID
	encoded, err := json.Marshal(args)
	if err != nil {
		return call
	}
	call.Arguments = string(encoded)
	return call
}

func buildParallelEditableWorkerPatchCall(ownerNodeID string, patch string) (ToolCall, error) {
	payload, err := json.Marshal(map[string]any{
		"patch":         patch,
		"owner_node_id": strings.TrimSpace(ownerNodeID),
	})
	if err != nil {
		return ToolCall{}, err
	}
	return ToolCall{
		Name:      "apply_patch",
		Arguments: string(payload),
	}, nil
}

func summarizeParallelEditableWorkerApplyResult(result ToolExecutionResult) string {
	paths := toolMetaStringSlice(result.Meta, "changed_paths")
	count := toolMetaInt(result.Meta, "changed_count")
	if len(paths) > 0 {
		preview := paths
		if len(preview) > 3 {
			preview = preview[:3]
		}
		summary := fmt.Sprintf("Parallel editable worker changed %d file(s): %s", max(count, len(paths)), strings.Join(preview, ", "))
		if len(paths) > len(preview) {
			summary += fmt.Sprintf(" (+%d more)", len(paths)-len(preview))
		}
		return summary
	}
	return firstNonBlankString(summarizeEditToolResult("apply_patch", result.DisplayText), "Parallel editable worker applied a patch.")
}

func summarizeParallelEditableWorkerNoop(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "Parallel editable worker inspected the leased edit scope without applying a patch."
	}
	return compactPromptSection(trimmed, 180)
}

func extractPatchDocument(text string) string {
	normalized := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	start := strings.Index(normalized, "*** Begin Patch")
	if start < 0 {
		return ""
	}
	end := strings.Index(normalized[start:], "*** End Patch")
	if end < 0 {
		return ""
	}
	patch := normalized[start : start+end+len("*** End Patch")]
	if !strings.HasSuffix(patch, "\n") {
		patch += "\n"
	}
	return patch
}

func stripPatchFromText(text string) string {
	patch := extractPatchDocument(text)
	if patch == "" {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(strings.Replace(strings.ReplaceAll(text, "\r\n", "\n"), patch, "", 1))
}

func parallelEditableVerificationRoot(a *Agent, node TaskNode) string {
	if a == nil {
		return ""
	}
	if strings.TrimSpace(node.EditableWorktreeRoot) != "" {
		return strings.TrimSpace(node.EditableWorktreeRoot)
	}
	if a.Session != nil && a.Session.TaskGraph != nil {
		if updated, ok := a.Session.TaskGraph.Node(node.ID); ok && strings.TrimSpace(updated.EditableWorktreeRoot) != "" {
			return strings.TrimSpace(updated.EditableWorktreeRoot)
		}
	}
	return firstNonBlankString(a.Workspace.Root, a.Workspace.BaseRoot)
}

func backgroundVerificationCommandsFromPlan(plan VerificationPlan, limit int) []string {
	commands := make([]string, 0, min(limit, len(plan.Steps)))
	for _, step := range plan.Steps {
		command := strings.TrimSpace(step.Command)
		if command == "" {
			continue
		}
		commands = append(commands, command)
		if limit > 0 && len(commands) >= limit {
			break
		}
	}
	return normalizeBackgroundCommandList(commands, limit)
}

func parallelEditableVerificationStartOptionalError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(lower, "unknown tool: run_shell_bundle_background") ||
		strings.Contains(lower, "background jobs are not configured")
}

func filterParallelWorkerNodeIDs(nodeIDs []string, excluded []string) []string {
	if len(excluded) == 0 {
		return append([]string(nil), nodeIDs...)
	}
	skip := map[string]struct{}{}
	for _, item := range excluded {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			skip[trimmed] = struct{}{}
		}
	}
	out := make([]string, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		if _, exists := skip[strings.TrimSpace(nodeID)]; exists {
			continue
		}
		out = append(out, nodeID)
	}
	return out
}

func filterParallelWorkerGuidance(guidance []string, excluded []string) []string {
	if len(excluded) == 0 {
		return append([]string(nil), guidance...)
	}
	skip := map[string]struct{}{}
	for _, item := range excluded {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			skip[trimmed] = struct{}{}
		}
	}
	out := make([]string, 0, len(guidance))
	for _, item := range guidance {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		nodeID := trimmed
		if index := strings.Index(trimmed, ":"); index >= 0 {
			nodeID = strings.TrimSpace(trimmed[:index])
		}
		if _, exists := skip[nodeID]; exists {
			continue
		}
		out = append(out, item)
	}
	return out
}

func (a *Agent) completeModelTurnWithClient(ctx context.Context, client ProviderClient, req ChatRequest) (ChatResponse, error) {
	if client == nil {
		return ChatResponse{}, fmt.Errorf("no model provider is configured")
	}
	maxRetries := configMaxRequestRetries(a.Config)
	totalAttempts := maxRetries + 1
	baseDelay := configRequestRetryDelay(a.Config)
	for attempt := 0; attempt < totalAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return ChatResponse{}, err
		}

		attemptCtx, cancel := context.WithTimeout(ctx, configRequestTimeout(a.Config))
		resp, err := completeModelTurnOnceWithClient(attemptCtx, client, req)
		cancel()
		if err == nil {
			return resp, nil
		}
		if ctx.Err() != nil {
			return ChatResponse{}, ctx.Err()
		}
		if !shouldRetryProviderError(err) || attempt == totalAttempts-1 {
			return ChatResponse{}, err
		}
		delay := providerRetryDelay(baseDelay, attempt)
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ChatResponse{}, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return ChatResponse{}, context.DeadlineExceeded
}

func completeModelTurnOnceWithClient(ctx context.Context, client ProviderClient, req ChatRequest) (ChatResponse, error) {
	type result struct {
		resp ChatResponse
		err  error
	}

	done := make(chan result, 1)
	go func() {
		resp, err := client.Complete(ctx, req)
		done <- result{resp: resp, err: err}
	}()

	select {
	case <-ctx.Done():
		return ChatResponse{}, ctx.Err()
	case out := <-done:
		return out.resp, out.err
	}
}
