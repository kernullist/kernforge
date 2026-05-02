package main

import (
	"strings"
)

func (a *Agent) recordEditLoopToolResult(call ToolCall, result ToolExecutionResult, execErr error) {
	if a == nil || a.Session == nil {
		return
	}
	meta := result.Meta
	effect := strings.TrimSpace(strings.ToLower(toolMetaString(meta, "effect")))
	paths := toolMetaStringSlice(meta, "changed_paths")
	if len(paths) == 0 && isEditTool(call.Name) {
		if path := strings.TrimSpace(toolMetaString(meta, "path")); path != "" {
			paths = []string{path}
		}
	}
	verificationLike := toolMetaBool(meta, "verification_like") || (strings.EqualFold(strings.TrimSpace(call.Name), "run_shell") && runShellOutputLooksLikeVerification(result.DisplayText))
	editLike := isEditTool(call.Name) || effect == "edit" || toolMetaBool(meta, "changed_workspace") || len(paths) > 0
	if !editLike && !verificationLike {
		return
	}
	summary := summarizeToolInvocation(a.Config, call)
	if summary == "" {
		summary = strings.TrimSpace(call.Name)
	}
	source := "main"
	if toolMetaBool(meta, "parallel_edit_worker") {
		source = "parallel-edit-worker"
	} else if toolMetaBool(meta, "parallel_worker") {
		source = "parallel-worker"
	}
	kind := "apply"
	if strings.Contains(source, "worker") {
		kind = "worker_apply"
	}
	status := editLoopToolEventStatus(call, meta, execErr, verificationLike)
	if verificationLike && !editLike {
		kind = "verification"
	}
	detail := strings.TrimSpace(result.DisplayText)
	if execErr != nil {
		detail = joinSentence(detail, execErr.Error())
	}
	bundleID := toolMetaString(meta, "bundle_id")
	jobIDs := toolMetaStringSlice(meta, "bundle_job_ids")
	if jobID := toolMetaString(meta, "job_id"); jobID != "" {
		jobIDs = normalizeTaskStateList(append(jobIDs, jobID), 32)
	}
	fingerprint := ""
	if status == "failed" || status == "error" {
		fingerprint = editLoopFailureFingerprint(firstNonBlankString(detail, summary))
	}
	loop := a.Session.RecordEditLoopEvent(editLoopGoal(a.Session), EditLoopEvent{
		Kind:                    kind,
		Source:                  source,
		ToolName:                call.Name,
		OwnerNodeID:             firstNonBlankString(toolMetaString(meta, "owner_node_id"), currentExecutorFocusNode(a.Session)),
		Summary:                 summary,
		Detail:                  compactPromptSection(detail, 500),
		Status:                  status,
		ChangedPaths:            normalizeTaskStateList(paths, 32),
		LeasePaths:              toolMetaStringSlice(meta, "owner_lease_paths"),
		PatchTransactionID:      toolMetaString(meta, "patch_transaction_id"),
		PatchTransactionEntryID: toolMetaString(meta, "patch_transaction_entry_id"),
		BundleID:                bundleID,
		JobIDs:                  jobIDs,
		LogPaths:                editLoopLogPathsFromMeta(meta),
		FailureFingerprint:      fingerprint,
	})
	if loop != nil && verificationLike && status == "failed" {
		loop.RemainingRisks = appendTaskStateItem(loop.RemainingRisks, "Verification-like command failed: "+summary, 12)
	}
	if loop != nil && bundleID != "" && strings.Contains(source, "worker") {
		loop.LinkWorkerVerificationBundle(toolMetaString(meta, "owner_node_id"), bundleID)
	}
}

func (a *Agent) recordEditLoopVerification(report VerificationReport) {
	if a == nil || a.Session == nil {
		return
	}
	status := "passed"
	if report.HasFailures() {
		status = "failed"
	}
	summary := report.SummaryLine()
	if report.HasFailures() {
		summary = report.FailureSummary()
	}
	loop := a.Session.RecordEditLoopEvent(editLoopGoal(a.Session), EditLoopEvent{
		Kind:               "verification",
		Source:             "auto",
		ToolName:           "verify",
		OwnerNodeID:        currentExecutorFocusNode(a.Session),
		Summary:            compactPromptSection(summary, 420),
		Detail:             compactPromptSection(report.RenderShort(), 700),
		Status:             status,
		ChangedPaths:       normalizeTaskStateList(report.ChangedPaths, 32),
		FailureFingerprint: editLoopFailureFingerprint(summary),
	})
	if loop == nil {
		return
	}
	loop.VerificationStatus = status
	loop.VerificationSummary = compactPromptSection(summary, 500)
	if guidance := strings.TrimSpace(report.RepairGuidance()); guidance != "" {
		loop.RepairGuidance = guidance
	}
	if !report.HasFailures() {
		loop.RemainingRisks = removeMatchingTaskStateItem(loop.RemainingRisks, "verification")
	}
	loop.Normalize()
}

func (a *Agent) recordEditLoopRetry(summary string, detail string) *EditLoopRetryDecision {
	if a == nil || a.Session == nil {
		return nil
	}
	loop := a.Session.RecordEditLoopEvent(editLoopGoal(a.Session), EditLoopEvent{
		Kind:               "retry",
		Source:             "controller",
		ToolName:           "verify",
		OwnerNodeID:        currentExecutorFocusNode(a.Session),
		Summary:            compactPromptSection(summary, 240),
		Detail:             compactPromptSection(detail, 500),
		Status:             "planned",
		FailureFingerprint: editLoopFailureFingerprint(detail),
	})
	if loop != nil && len(loop.RetryDecisions) > 0 {
		decision := loop.RetryDecisions[len(loop.RetryDecisions)-1]
		if decision.Action == "change_strategy" || decision.Action == "escalate_reviewer" {
			loop.RepairGuidance = appendEditLoopGuidance(loop.RepairGuidance, decision.Reason)
		}
		return &decision
	}
	return nil
}

func (a *Agent) recordEditLoopRisk(summary string, detail string) {
	if a == nil || a.Session == nil {
		return
	}
	a.Session.RecordEditLoopEvent(editLoopGoal(a.Session), EditLoopEvent{
		Kind:        "risk",
		Source:      "controller",
		OwnerNodeID: currentExecutorFocusNode(a.Session),
		Summary:     compactPromptSection(summary, 280),
		Detail:      compactPromptSection(detail, 500),
		Status:      "open",
	})
}

func (a *Agent) recordEditLoopFinalReview(verdict string, feedback string) {
	if a == nil || a.Session == nil || a.Session.ActiveEditLoop == nil {
		return
	}
	a.Session.RecordEditLoopEvent(editLoopGoal(a.Session), EditLoopEvent{
		Kind:    "final_review",
		Source:  "reviewer",
		Summary: compactPromptSection(feedback, 280),
		Detail:  compactPromptSection(feedback, 700),
		Status:  strings.TrimSpace(strings.ToLower(verdict)),
	})
}

func (a *Agent) finalizeEditLoopOnReturn(reply string, unresolvedVerification bool) {
	if a == nil || a.Session == nil || a.Session.ActiveEditLoop == nil {
		return
	}
	loop := a.Session.ActiveEditLoop
	if len(loop.ChangedPaths) == 0 && len(loop.WorkerSummaries) == 0 && loop.VerificationSummary == "" && len(loop.RemainingRisks) == 0 && len(loop.Events) == 0 {
		return
	}
	status := editLoopStatusCompleted
	if unresolvedVerification || strings.EqualFold(loop.VerificationStatus, "failed") {
		status = editLoopStatusRiskAccepted
	}
	if loop.VerificationStatus == "" && len(loop.ChangedPaths) > 0 && !replyMentionsVerificationNotRun(reply) && !sessionHasSuccessfulVerificationEvidence(a.Session) {
		loop.RemainingRisks = appendTaskStateItem(loop.RemainingRisks, "No successful verification was recorded for the changed paths.", 12)
		if status == editLoopStatusCompleted {
			status = editLoopStatusRiskAccepted
		}
	}
	if len(loop.RemainingRisks) > 0 {
		status = editLoopStatusRiskAccepted
	}
	loop.RecordEvent(EditLoopEvent{
		Kind:    "final_answer",
		Source:  "main",
		Summary: compactPromptSection(reply, 360),
		Status:  status,
	})
	loop.UpdateOutcomeContract(reply, status)
	a.Session.FinalizeActiveEditLoop(status)
}

func editLoopToolEventStatus(call ToolCall, meta map[string]any, execErr error, verificationLike bool) string {
	if execErr != nil {
		if verificationLike {
			return "failed"
		}
		return "error"
	}
	if bundleStatus := strings.TrimSpace(strings.ToLower(toolMetaString(meta, "bundle_status"))); bundleStatus != "" {
		switch bundleStatus {
		case "completed":
			if toolMetaInt(meta, "failed") > 0 || !toolMetaBoolDefault(meta, "success", true) {
				return "failed"
			}
			return "passed"
		case "failed":
			return "failed"
		case "running", "stale", "superseded", "canceled", "preempted":
			return bundleStatus
		default:
			return bundleStatus
		}
	}
	if jobStatus := strings.TrimSpace(strings.ToLower(toolMetaString(meta, "job_status"))); jobStatus != "" {
		switch jobStatus {
		case "completed":
			if !toolMetaBoolDefault(meta, "success", true) {
				return "failed"
			}
			if verificationLike {
				return "passed"
			}
			return "ok"
		case "failed":
			return "failed"
		case "running", "stale", "superseded", "canceled", "preempted":
			return jobStatus
		default:
			return jobStatus
		}
	}
	if verificationLike {
		if !toolMetaBoolDefault(meta, "success", true) {
			return "failed"
		}
		return "passed"
	}
	return "ok"
}

func editLoopLogPathsFromMeta(meta map[string]any) []string {
	paths := make([]string, 0)
	if logPath := toolMetaString(meta, "log_path"); logPath != "" {
		paths = append(paths, logPath)
	}
	raw, ok := meta["job_status"]
	if !ok {
		return normalizeTaskStateList(paths, 32)
	}
	appendFromMap := func(item map[string]any) {
		if logPath := toolMetaString(item, "log_path"); logPath != "" {
			paths = append(paths, logPath)
		}
	}
	if items, ok := raw.([]map[string]any); ok {
		for _, item := range items {
			appendFromMap(item)
		}
		return normalizeTaskStateList(paths, 32)
	}
	if list, ok := raw.([]any); ok {
		for _, item := range list {
			if entry, ok := item.(map[string]any); ok {
				appendFromMap(entry)
			}
		}
	}
	return normalizeTaskStateList(paths, 32)
}

func appendEditLoopGuidance(current string, next string) string {
	current = strings.TrimSpace(current)
	next = strings.TrimSpace(next)
	if next == "" {
		return current
	}
	if current == "" {
		return next
	}
	if strings.Contains(strings.ToLower(current), strings.ToLower(next)) {
		return current
	}
	return current + "\n" + next
}

func editLoopRetryDecisionPrompt(decision EditLoopRetryDecision) string {
	decision.Normalize()
	if decision.Action == "" {
		return ""
	}
	switch decision.Action {
	case "change_strategy":
		return "- The same verification failure has repeated. Do not retry the same repair path; inspect the first concrete failure, make a materially different minimal fix, then rerun the narrowest relevant verification."
	case "escalate_reviewer":
		return "- The same verification failure has repeated multiple times. Ask for or use reviewer guidance before another retry, and clearly state the blocker if no materially different repair path is available."
	default:
		return ""
	}
}

func currentExecutorFocusNode(sess *Session) string {
	if sess == nil || sess.TaskState == nil {
		return ""
	}
	return strings.TrimSpace(sess.TaskState.ExecutorFocusNode)
}

func removeMatchingTaskStateItem(items []string, needle string) []string {
	needle = strings.TrimSpace(strings.ToLower(needle))
	if needle == "" || len(items) == 0 {
		return items
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if strings.Contains(strings.ToLower(strings.TrimSpace(item)), needle) {
			continue
		}
		out = append(out, item)
	}
	return normalizeTaskStateList(out, 12)
}
