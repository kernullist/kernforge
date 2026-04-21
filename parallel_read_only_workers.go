package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

const parallelReadOnlyWorkerCooldown = 8 * time.Second

type parallelReadOnlyWorkerPlan struct {
	Node       TaskNode
	Call       ToolCall
	Reason     string
	Specialist string
	RouteHint  string
}

type parallelReadOnlyWorkerResult struct {
	NodeID string
	Call   ToolCall
	Result ToolExecutionResult
	Err    error
}

func (a *Agent) maybeRunInteractiveParallelReadOnlyWorkers(ctx context.Context, trigger string) error {
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
	if !shouldRunInteractiveMicroWorkers(trigger, graph) {
		return nil
	}
	candidates := a.executorAwareReadOnlyWorkerCandidates(2)
	if len(candidates) == 0 {
		return nil
	}
	updated := false
	plans := make([]parallelReadOnlyWorkerPlan, 0, len(candidates))
	for _, node := range candidates {
		assignment, hasAssignment := selectSpecialistForTaskNode(a.Config, node, a.Session.TaskState, trigger, true)
		plan, ok := buildParallelReadOnlyWorkerPlan(a.Session, node, assignment)
		if !ok {
			continue
		}
		if hasAssignment {
			a.Session.RecordTaskGraphSpecialistAssignment(node.ID, assignment.Profile.Name, assignment.Reason)
			updated = true
		}
		plans = append(plans, plan)
	}
	if len(plans) == 0 {
		return nil
	}

	state := a.Session.EnsureTaskState()
	for _, plan := range plans {
		state.RecordEvent("parallel_worker_start", plan.Node.ID, plan.Call.Name, "Parallel read-only worker started for "+plan.Node.Title, plan.Reason, "started", false)
		updated = true
	}

	results := make(chan parallelReadOnlyWorkerResult, len(plans))
	var wg sync.WaitGroup
	for _, plan := range plans {
		plan := plan
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := a.Tools.ExecuteDetailed(ctx, plan.Call.Name, plan.Call.Arguments)
			if result.Meta == nil {
				result.Meta = map[string]any{}
			}
			result.Meta["owner_node_id"] = plan.Node.ID
			result.Meta["parallel_worker"] = true
			results <- parallelReadOnlyWorkerResult{
				NodeID: plan.Node.ID,
				Call:   plan.Call,
				Result: result,
				Err:    err,
			}
		}()
	}
	wg.Wait()
	close(results)

	for item := range results {
		if item.Err != nil {
			a.Session.RecordPlanNodeFailure(item.NodeID, item.Call.Name, item.Err.Error())
			state.RecordEvent("parallel_worker_error", item.NodeID, item.Call.Name, "Parallel read-only worker failed for "+item.NodeID, item.Err.Error(), "error", false)
			updated = true
			continue
		}
		summary := summarizeParallelReadOnlyWorkerResult(item.Call.Name, item.Result)
		a.Session.RecordTaskGraphReadOnlyWorkerEvidence(item.NodeID, item.Call.Name, summary)
		state.RecordEvent("parallel_worker_result", item.NodeID, item.Call.Name, summary, compactPromptSection(item.Result.DisplayText, 220), "ok", false)
		policy := buildToolExecutionPolicy(item.Call, item.Result, a.Session)
		if a.applyToolExecutionPolicy(policy, item.Call, firstNonBlankString(summary, item.Call.Name), item.Result.DisplayText) {
			updated = true
		}
		updated = true
	}
	if updated && a.Store != nil {
		return a.Store.Save(a.Session)
	}
	return nil
}

func (a *Agent) executorAwareReadOnlyWorkerCandidates(limit int) []TaskNode {
	if a == nil || a.Session == nil || a.Session.TaskGraph == nil || a.Session.TaskState == nil {
		return nil
	}
	candidates := make([]TaskNode, 0, limit)
	seen := map[string]struct{}{}
	for _, nodeID := range a.Session.TaskState.ExecutorParallelNodes {
		node, ok := a.Session.TaskGraph.Node(nodeID)
		if !ok || !taskNodeEligibleForParallelReadOnlyWorker(node) {
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

func taskNodeEligibleForParallelReadOnlyWorker(node TaskNode) bool {
	if !isPrimaryTaskNode(node) {
		return false
	}
	status := canonicalTaskNodeStatus(node.Status)
	if status == "completed" || status == "failed" || status == "stale" || status == "superseded" || status == "canceled" || status == "preempted" {
		return false
	}
	if !node.ReadOnlyWorkerAt.IsZero() && time.Since(node.ReadOnlyWorkerAt) < parallelReadOnlyWorkerCooldown {
		return false
	}
	return true
}

func buildParallelReadOnlyWorkerPlan(session *Session, node TaskNode, assignment SpecialistAssignment) (parallelReadOnlyWorkerPlan, bool) {
	makePlan := func(toolName string, args map[string]any, reason string) (parallelReadOnlyWorkerPlan, bool) {
		payload := cloneMetaMap(args)
		payload["owner_node_id"] = node.ID
		raw, err := json.Marshal(payload)
		if err != nil {
			return parallelReadOnlyWorkerPlan{}, false
		}
		return parallelReadOnlyWorkerPlan{
			Node: node,
			Call: ToolCall{
				Name:      toolName,
				Arguments: string(raw),
			},
			Reason:     reason,
			Specialist: strings.TrimSpace(assignment.Profile.Name),
			RouteHint:  strings.TrimSpace(assignment.Reason),
		}, true
	}

	if bundleID := preferredBundleForParallelWorker(session, node); bundleID != "" {
		return makePlan("check_shell_bundle", map[string]any{
			"bundle_id": bundleID,
		}, "Poll the linked background bundle instead of duplicating the same verification command.")
	}
	if jobID := preferredJobForParallelWorker(session, node); jobID != "" {
		return makePlan("check_shell_job", map[string]any{
			"job_id": jobID,
		}, "Poll the linked background job to gather the latest evidence for this node.")
	}
	if plan, ok := buildParallelEditableWarmupPlan(node, assignment, makePlan); ok {
		return plan, true
	}

	text := strings.ToLower(strings.Join([]string{
		node.Title,
		node.LifecycleNote,
		node.MicroWorkerBrief,
		node.LastFailure,
	}, " "))
	switch {
	case strings.Contains(text, "git diff"), strings.Contains(text, "diff"), strings.Contains(text, "patch"):
		return makePlan("git_diff", map[string]any{}, "Inspect the current diff for this secondary node without mutating the workspace.")
	case strings.Contains(text, "git status"), strings.Contains(text, "branch"), strings.Contains(text, "staged"), strings.Contains(text, "commit"):
		return makePlan("git_status", map[string]any{}, "Inspect the current git status for this secondary node.")
	}

	if pattern := buildParallelReadOnlyWorkerSearchPattern(node, assignment.Profile.Keywords); pattern != "" {
		return makePlan("grep", map[string]any{
			"pattern":     pattern,
			"max_results": 20,
		}, "Search the workspace for the most relevant evidence tied to this secondary node.")
	}

	return makePlan("list_files", map[string]any{
		"path":        ".",
		"recursive":   false,
		"max_entries": 40,
	}, "Scan a small directory slice so the executor has current file-level context for this node.")
}

func buildParallelEditableWarmupPlan(node TaskNode, assignment SpecialistAssignment, makePlan func(string, map[string]any, string) (parallelReadOnlyWorkerPlan, bool)) (parallelReadOnlyWorkerPlan, bool) {
	if !strings.EqualFold(strings.TrimSpace(node.Kind), "edit") {
		return parallelReadOnlyWorkerPlan{}, false
	}
	lease := bestEffortParallelEditableLeasePaths(node)
	if len(lease) == 0 {
		return parallelReadOnlyWorkerPlan{}, false
	}
	if concretePath := firstConcreteEditableLeasePath(lease); concretePath != "" {
		return makePlan("read_file", map[string]any{
			"path": concretePath,
		}, "Inspect the leased edit file so the secondary edit node stays scoped to its ownership boundary.")
	}
	baseDir := firstEditableLeaseBaseDir(lease)
	if baseDir == "" {
		return parallelReadOnlyWorkerPlan{}, false
	}
	if pattern := buildParallelReadOnlyWorkerSearchPattern(node, assignment.Profile.Keywords); pattern != "" {
		return makePlan("grep", map[string]any{
			"pattern":     pattern,
			"path":        baseDir,
			"max_results": 20,
		}, "Search only inside the leased edit scope so this secondary edit node stays warm without crossing ownership boundaries.")
	}
	return makePlan("list_files", map[string]any{
		"path":        baseDir,
		"recursive":   true,
		"max_entries": 40,
	}, "List files inside the leased edit scope so the executor can prepare the secondary edit lane safely.")
}

func preferredBundleForParallelWorker(session *Session, node TaskNode) string {
	if session == nil {
		return ""
	}
	for _, bundleID := range node.LinkedBundleIDs {
		bundle, ok := session.BackgroundBundle(bundleID)
		if !ok {
			continue
		}
		status := strings.TrimSpace(strings.ToLower(bundle.Status))
		if status == "running" || status == "failed" || status == "completed" {
			return bundle.ID
		}
	}
	return ""
}

func preferredJobForParallelWorker(session *Session, node TaskNode) string {
	if session == nil {
		return ""
	}
	for _, jobID := range node.LinkedJobIDs {
		job, ok := session.BackgroundJob(jobID)
		if !ok {
			continue
		}
		status := strings.TrimSpace(strings.ToLower(job.Status))
		if status == "running" || status == "failed" || status == "completed" {
			return job.ID
		}
	}
	return ""
}

func buildParallelReadOnlyWorkerSearchPattern(node TaskNode, specialistKeywords []string) string {
	terms := extractParallelReadOnlyWorkerTerms(strings.Join([]string{
		node.Title,
		node.LifecycleNote,
		node.MicroWorkerBrief,
		node.LastFailure,
	}, " "))
	for _, keyword := range specialistKeywords {
		trimmed := strings.TrimSpace(keyword)
		if trimmed == "" {
			continue
		}
		terms = append(terms, trimmed)
	}
	terms = uniqueStrings(terms)
	if len(terms) == 0 {
		return ""
	}
	if len(terms) > 3 {
		terms = terms[:3]
	}
	parts := make([]string, 0, len(terms))
	for _, term := range terms {
		parts = append(parts, regexp.QuoteMeta(term))
	}
	if len(parts) == 1 {
		return "(?i)" + parts[0]
	}
	return "(?i)(" + strings.Join(parts, "|") + ")"
}

func extractParallelReadOnlyWorkerTerms(text string) []string {
	stopwords := map[string]struct{}{
		"about": {}, "after": {}, "before": {}, "blocker": {}, "bundle": {}, "check": {}, "code": {}, "current": {},
		"evidence": {}, "executor": {}, "final": {}, "focus": {}, "latest": {}, "needs": {}, "next": {}, "node": {},
		"output": {}, "parallel": {}, "plan": {}, "read": {}, "ready": {}, "result": {}, "retry": {}, "running": {},
		"secondary": {}, "state": {}, "step": {}, "task": {}, "worker": {}, "workspace": {},
	}
	rawTerms := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' && r != '-' && r != '.'
	})
	out := make([]string, 0, 4)
	seen := map[string]struct{}{}
	for _, term := range rawTerms {
		term = strings.TrimSpace(term)
		if len(term) < 4 {
			continue
		}
		if _, blocked := stopwords[term]; blocked {
			continue
		}
		if _, exists := seen[term]; exists {
			continue
		}
		seen[term] = struct{}{}
		out = append(out, term)
		if len(out) >= 4 {
			break
		}
	}
	return out
}

func summarizeParallelReadOnlyWorkerResult(toolName string, result ToolExecutionResult) string {
	meta := result.Meta
	switch strings.TrimSpace(toolName) {
	case "check_shell_bundle":
		return firstNonBlankString(toolMetaString(meta, "bundle_summary"), truncateStatusSnippet(firstNonEmptyLine(result.DisplayText), 160))
	case "check_shell_job":
		jobStatus := firstNonBlankString(toolMetaString(meta, "job_status"), "background job")
		return fmt.Sprintf("%s: %s", jobStatus, truncateStatusSnippet(firstNonEmptyLine(result.DisplayText), 140))
	case "grep":
		matchCount := toolMetaInt(meta, "match_count")
		fileCount := toolMetaInt(meta, "file_count")
		pattern := compactPromptSection(toolMetaString(meta, "pattern"), 60)
		if matchCount == 0 {
			return fmt.Sprintf("grep found no matches for %s", pattern)
		}
		return fmt.Sprintf("grep found %d matches in %d file(s) for %s", matchCount, fileCount, pattern)
	case "list_files":
		entryCount := toolMetaInt(meta, "entry_count")
		path := firstNonBlankString(toolMetaString(meta, "path"), ".")
		return fmt.Sprintf("listed %d entries under %s", entryCount, path)
	case "git_status":
		if toolMetaBool(meta, "clean") {
			return "git status reports a clean working tree"
		}
		return fmt.Sprintf("git status shows %d changed path(s) on %s", toolMetaInt(meta, "changed_count"), firstNonBlankString(toolMetaString(meta, "branch"), "the current branch"))
	case "git_diff":
		if !toolMetaBool(meta, "has_diff") {
			return "git diff is empty"
		}
		return fmt.Sprintf("git diff shows changes in %d file(s)", toolMetaInt(meta, "file_count"))
	default:
		return truncateStatusSnippet(firstNonEmptyLine(result.DisplayText), 160)
	}
}
