package main

import (
	"strings"
)

func (a *Agent) recordEditLoopToolResult(call ToolCall, result ToolExecutionResult, execErr error) {
	if a == nil || a.Session == nil {
		return
	}
	meta := result.Meta
	paths := toolMetaStringSlice(meta, "changed_paths")
	if len(paths) == 0 && isEditTool(call.Name) {
		if path := strings.TrimSpace(toolMetaString(meta, "path")); path != "" {
			paths = []string{path}
		}
	}
	verificationLike := toolResultLooksLikeVerificationAttempt(call.Name, meta, result.DisplayText)
	editLike := toolResultRepresentsWorkspaceEdit(call.Name, meta)
	if !editLike && execErr != nil && toolResultAttemptedWorkspaceEdit(call.Name, meta) {
		editLike = true
	}
	if !editLike && !verificationLike {
		return
	}
	summary := summarizeToolInvocation(a.Config, call)
	if summary == "" {
		summary = strings.TrimSpace(call.Name)
	}
	riskSummary := summary
	if verificationLike {
		riskSummary = editLoopVerificationRiskSummary(summary, meta)
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
	if loop != nil && verificationLike {
		switch status {
		case "passed":
			loop.RemainingRisks = removeMatchingTaskStateItem(loop.RemainingRisks, "No successful verification")
			for _, risk := range []string{
				"Verification failed: " + riskSummary,
				"Verification-like command failed: " + riskSummary,
				"Verification-like command was skipped: " + riskSummary,
				"Verification-like command did not produce successful evidence: " + riskSummary,
			} {
				loop.RemainingRisks = removeExactTaskStateItem(loop.RemainingRisks, risk)
			}
		case "failed", "error":
			loop.RemainingRisks = removeExactTaskStateItem(loop.RemainingRisks, "Verification failed: "+summary)
			loop.RemainingRisks = appendTaskStateItem(loop.RemainingRisks, "Verification-like command failed: "+riskSummary, 12)
		case "skipped":
			loop.RemainingRisks = appendTaskStateItem(loop.RemainingRisks, "Verification-like command was skipped: "+riskSummary, 12)
		case "running", "pending", "stale", "superseded", "canceled", "cancelled", "preempted":
			loop.RemainingRisks = appendTaskStateItem(loop.RemainingRisks, "Verification-like command did not produce successful evidence: "+riskSummary, 12)
		}
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
	} else if report.WasSkipped() {
		status = "skipped"
	}
	summary := report.SummaryLine()
	if report.HasFailures() {
		summary = report.FailureSummary()
	} else if report.WasSkipped() && strings.TrimSpace(report.Decision) != "" {
		summary = report.Decision
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
	if report.WasSkipped() {
		loop.RemainingRisks = appendTaskStateItem(loop.RemainingRisks, "Automatic verification was skipped: "+summary, 12)
	} else if !report.HasFailures() {
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
	if unresolvedVerification || editLoopVerificationStatusUnresolved(loop.VerificationStatus) {
		status = editLoopStatusRiskAccepted
	}
	if len(loop.ChangedPaths) > 0 && !sessionHasSuccessfulVerificationEvidence(a.Session) &&
		(strings.TrimSpace(loop.VerificationStatus) == "" || editLoopVerificationStatusUnresolved(loop.VerificationStatus)) {
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

func editLoopVerificationStatusUnresolved(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "", "passed":
		return false
	default:
		return true
	}
}

func editLoopToolEventStatus(call ToolCall, meta map[string]any, execErr error, verificationLike bool) string {
	if execErr != nil {
		if verificationLike {
			return "failed"
		}
		return "error"
	}
	if status := toolMetaExplicitVerificationStatus(meta); status != "" {
		switch status {
		case VerificationPassed:
			if conflict := editLoopVerificationPassedConflictStatus(meta); conflict != "" {
				return conflict
			}
			return "passed"
		case VerificationFailed:
			return "failed"
		case VerificationSkipped:
			return "skipped"
		case VerificationPending:
			return "running"
		}
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
	if status := toolMetaCommandExecutionVerificationStatus(meta); status != "" {
		switch status {
		case VerificationPassed:
			if !toolMetaBoolDefault(meta, "success", true) {
				return "failed"
			}
			return "passed"
		case VerificationFailed:
			return "failed"
		case VerificationSkipped:
			return "skipped"
		case VerificationPending:
			return "running"
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

func editLoopVerificationPassedConflictStatus(meta map[string]any) string {
	if !toolMetaBoolDefault(meta, "success", true) {
		return "failed"
	}
	if bundleStatus := strings.TrimSpace(strings.ToLower(toolMetaString(meta, "bundle_status"))); bundleStatus != "" {
		switch bundleStatus {
		case "completed":
			if toolMetaInt(meta, "failed") > 0 || !toolMetaBoolDefault(meta, "success", true) {
				return "failed"
			}
		case "failed":
			return "failed"
		case "running", "stale", "superseded", "canceled", "cancelled", "preempted":
			return bundleStatus
		}
	}
	if jobStatus := strings.TrimSpace(strings.ToLower(toolMetaString(meta, "job_status"))); jobStatus != "" {
		switch jobStatus {
		case "completed":
			if !toolMetaBoolDefault(meta, "success", true) {
				return "failed"
			}
		case "failed":
			return "failed"
		case "running", "stale", "superseded", "canceled", "cancelled", "preempted":
			return jobStatus
		}
	}
	if status := toolMetaCommandExecutionVerificationStatus(meta); status != "" {
		switch status {
		case VerificationFailed:
			return "failed"
		case VerificationSkipped:
			return "skipped"
		case VerificationPending:
			return "running"
		}
	}
	return ""
}

func editLoopVerificationRiskSummary(fallback string, meta map[string]any) string {
	for _, key := range []string{"command", "resolved_command", "label", "summary"} {
		if value := strings.TrimSpace(toolMetaString(meta, key)); value != "" {
			return compactPromptSection(value, 240)
		}
	}
	return compactPromptSection(strings.TrimSpace(fallback), 240)
}

func editLoopLogPathsFromMeta(meta map[string]any) []string {
	paths := make([]string, 0)
	if logPath := toolMetaString(meta, "log_path"); logPath != "" {
		paths = append(paths, logPath)
	}
	appendFromMap := func(item map[string]any) {
		if logPath := toolMetaString(item, "log_path"); logPath != "" {
			paths = append(paths, logPath)
		}
	}
	appendFromList := func(raw any) {
		if items, ok := raw.([]map[string]any); ok {
			for _, item := range items {
				appendFromMap(item)
			}
			return
		}
		if list, ok := raw.([]any); ok {
			for _, item := range list {
				if entry, ok := item.(map[string]any); ok {
					appendFromMap(entry)
				}
			}
		}
	}
	for _, key := range []string{"job_entries", "jobs", "job_status"} {
		if raw, ok := meta[key]; ok {
			appendFromList(raw)
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

func removeExactTaskStateItem(items []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || len(items) == 0 {
		return items
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), value) {
			continue
		}
		out = append(out, item)
	}
	return normalizeTaskStateList(out, 12)
}

func removeVerificationFailureRiskForPassedSummary(items []string, passedSummary string) []string {
	passedSummary = strings.TrimSpace(passedSummary)
	if passedSummary == "" || len(items) == 0 {
		return items
	}
	const prefix = "Verification failed: "
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		failedSummary := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
		if failedSummary != trimmed && verificationPassedSummaryMatchesFailure(failedSummary, passedSummary) {
			continue
		}
		out = append(out, item)
	}
	return normalizeTaskStateList(out, 12)
}

func verificationPassedSummaryMatchesFailure(failedSummary string, passedSummary string) bool {
	failedSummary = strings.TrimSpace(failedSummary)
	passedSummary = strings.TrimSpace(passedSummary)
	if failedSummary == "" || passedSummary == "" {
		return false
	}
	if strings.EqualFold(failedSummary, passedSummary) {
		return true
	}
	lowerFailed := strings.ToLower(failedSummary)
	lowerPassed := strings.ToLower(passedSummary)
	return strings.HasPrefix(lowerPassed, lowerFailed+" ") ||
		strings.HasPrefix(lowerPassed, lowerFailed+":") ||
		strings.HasPrefix(lowerPassed, lowerFailed+";")
}
