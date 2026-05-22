package main

import (
	"fmt"
	"strings"
	"time"
)

const (
	editLoopStatusActive       = "active"
	editLoopStatusReview       = "review"
	editLoopStatusCompleted    = "completed"
	editLoopStatusRiskAccepted = "risk_accepted"
	maxEditLoopEvents          = 36
	maxSessionEditLoops        = 8
)

type EditLoopState struct {
	ID                   string                         `json:"id,omitempty"`
	Goal                 string                         `json:"goal,omitempty"`
	Status               string                         `json:"status,omitempty"`
	AttemptCount         int                            `json:"attempt_count,omitempty"`
	RetryCount           int                            `json:"retry_count,omitempty"`
	ChangedPaths         []string                       `json:"changed_paths,omitempty"`
	WorkerSummaries      []string                       `json:"worker_summaries,omitempty"`
	VerificationSummary  string                         `json:"verification_summary,omitempty"`
	VerificationStatus   string                         `json:"verification_status,omitempty"`
	VerificationBundleID string                         `json:"verification_bundle_id,omitempty"`
	RepairGuidance       string                         `json:"repair_guidance,omitempty"`
	RemainingRisks       []string                       `json:"remaining_risks,omitempty"`
	FinalReviewVerdict   string                         `json:"final_review_verdict,omitempty"`
	FinalReviewFeedback  string                         `json:"final_review_feedback,omitempty"`
	StartedAt            time.Time                      `json:"started_at,omitempty"`
	UpdatedAt            time.Time                      `json:"updated_at,omitempty"`
	CompletedAt          time.Time                      `json:"completed_at,omitempty"`
	WorkerEvidence       []EditLoopWorkerEvidence       `json:"worker_evidence,omitempty"`
	VerificationEvidence []EditLoopVerificationEvidence `json:"verification_evidence,omitempty"`
	RetryDecisions       []EditLoopRetryDecision        `json:"retry_decisions,omitempty"`
	OutcomeContract      *EditLoopOutcomeContract       `json:"outcome_contract,omitempty"`
	Events               []EditLoopEvent                `json:"events,omitempty"`
}

type EditLoopEvent struct {
	Kind                    string    `json:"kind,omitempty"`
	Source                  string    `json:"source,omitempty"`
	ToolName                string    `json:"tool_name,omitempty"`
	OwnerNodeID             string    `json:"owner_node_id,omitempty"`
	Summary                 string    `json:"summary,omitempty"`
	Detail                  string    `json:"detail,omitempty"`
	Status                  string    `json:"status,omitempty"`
	ChangedPaths            []string  `json:"changed_paths,omitempty"`
	LeasePaths              []string  `json:"lease_paths,omitempty"`
	PatchTransactionID      string    `json:"patch_transaction_id,omitempty"`
	PatchTransactionEntryID string    `json:"patch_transaction_entry_id,omitempty"`
	BundleID                string    `json:"bundle_id,omitempty"`
	JobIDs                  []string  `json:"job_ids,omitempty"`
	LogPaths                []string  `json:"log_paths,omitempty"`
	FailureFingerprint      string    `json:"failure_fingerprint,omitempty"`
	RetryAction             string    `json:"retry_action,omitempty"`
	RecordedAt              time.Time `json:"recorded_at,omitempty"`
}

type EditLoopWorkerEvidence struct {
	Source                  string    `json:"source,omitempty"`
	ToolName                string    `json:"tool_name,omitempty"`
	OwnerNodeID             string    `json:"owner_node_id,omitempty"`
	Summary                 string    `json:"summary,omitempty"`
	Status                  string    `json:"status,omitempty"`
	ChangedPaths            []string  `json:"changed_paths,omitempty"`
	LeasePaths              []string  `json:"lease_paths,omitempty"`
	PatchTransactionID      string    `json:"patch_transaction_id,omitempty"`
	PatchTransactionEntryID string    `json:"patch_transaction_entry_id,omitempty"`
	VerificationBundleID    string    `json:"verification_bundle_id,omitempty"`
	RecordedAt              time.Time `json:"recorded_at,omitempty"`
}

type EditLoopVerificationEvidence struct {
	Source             string    `json:"source,omitempty"`
	ToolName           string    `json:"tool_name,omitempty"`
	OwnerNodeID        string    `json:"owner_node_id,omitempty"`
	Status             string    `json:"status,omitempty"`
	Summary            string    `json:"summary,omitempty"`
	Detail             string    `json:"detail,omitempty"`
	ChangedPaths       []string  `json:"changed_paths,omitempty"`
	BundleID           string    `json:"bundle_id,omitempty"`
	JobIDs             []string  `json:"job_ids,omitempty"`
	LogPaths           []string  `json:"log_paths,omitempty"`
	FailureFingerprint string    `json:"failure_fingerprint,omitempty"`
	RecordedAt         time.Time `json:"recorded_at,omitempty"`
}

type EditLoopRetryDecision struct {
	Attempt            int       `json:"attempt,omitempty"`
	Action             string    `json:"action,omitempty"`
	Reason             string    `json:"reason,omitempty"`
	FailureFingerprint string    `json:"failure_fingerprint,omitempty"`
	SameFailureCount   int       `json:"same_failure_count,omitempty"`
	RecordedAt         time.Time `json:"recorded_at,omitempty"`
}

type EditLoopOutcomeContract struct {
	Status                     string    `json:"status,omitempty"`
	ChangedPaths               []string  `json:"changed_paths,omitempty"`
	WorkerEvidenceCount        int       `json:"worker_evidence_count,omitempty"`
	VerificationEvidenceCount  int       `json:"verification_evidence_count,omitempty"`
	VerificationStatus         string    `json:"verification_status,omitempty"`
	VerificationSummary        string    `json:"verification_summary,omitempty"`
	RetryCount                 int       `json:"retry_count,omitempty"`
	RemainingRisks             []string  `json:"remaining_risks,omitempty"`
	FinalReviewVerdict         string    `json:"final_review_verdict,omitempty"`
	AnswerMentionsVerification bool      `json:"answer_mentions_verification,omitempty"`
	AnswerMentionsRisk         bool      `json:"answer_mentions_risk,omitempty"`
	UpdatedAt                  time.Time `json:"updated_at,omitempty"`
}

func (s *Session) EnsureEditLoop(goal string) *EditLoopState {
	if s == nil {
		return nil
	}
	now := time.Now()
	normalizedGoal := strings.Join(strings.Fields(strings.TrimSpace(goal)), " ")
	if s.ActiveEditLoop != nil &&
		!editLoopClosedStatus(s.ActiveEditLoop.Status) &&
		editLoopGoalsConflict(s.ActiveEditLoop.Goal, normalizedGoal) {
		s.FinalizeActiveEditLoop(editLoopStatusRiskAccepted)
	}
	if s.ActiveEditLoop == nil || editLoopClosedStatus(s.ActiveEditLoop.Status) {
		s.ActiveEditLoop = &EditLoopState{
			ID:        newEditLoopID(s, now),
			Goal:      normalizedGoal,
			Status:    editLoopStatusActive,
			StartedAt: now,
			UpdatedAt: now,
		}
	}
	if strings.TrimSpace(s.ActiveEditLoop.Goal) == "" {
		s.ActiveEditLoop.Goal = strings.Join(strings.Fields(strings.TrimSpace(goal)), " ")
	}
	s.ActiveEditLoop.Normalize()
	return s.ActiveEditLoop
}

func editLoopGoalsConflict(activeGoal string, nextGoal string) bool {
	active := normalizedPatchTransactionGoal(activeGoal)
	next := normalizedPatchTransactionGoal(nextGoal)
	if active == "" || next == "" || active == next {
		return false
	}
	return classifyTurnIntent(nextGoal) != TurnIntentContinueLastTask
}

func newEditLoopID(s *Session, now time.Time) string {
	base := fmt.Sprintf("edit-loop-%s", now.Format("20060102-150405.000000000"))
	if !sessionHasEditLoopID(s, base) {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !sessionHasEditLoopID(s, candidate) {
			return candidate
		}
	}
}

func sessionHasEditLoopID(s *Session, id string) bool {
	id = strings.TrimSpace(id)
	if s == nil || id == "" {
		return false
	}
	if s.ActiveEditLoop != nil && strings.TrimSpace(s.ActiveEditLoop.ID) == id {
		return true
	}
	for _, loop := range s.EditLoops {
		if strings.TrimSpace(loop.ID) == id {
			return true
		}
	}
	return false
}

func (s *Session) RecordEditLoopEvent(goal string, event EditLoopEvent) *EditLoopState {
	loop := s.EnsureEditLoop(goal)
	if loop == nil {
		return nil
	}
	loop.RecordEvent(event)
	return loop
}

func (s *Session) FinalizeActiveEditLoop(status string) {
	if s == nil || s.ActiveEditLoop == nil {
		return
	}
	now := time.Now()
	s.ActiveEditLoop.Status = strings.TrimSpace(strings.ToLower(status))
	if s.ActiveEditLoop.Status == "" {
		s.ActiveEditLoop.Status = editLoopStatusCompleted
	}
	s.ActiveEditLoop.UpdatedAt = now
	s.ActiveEditLoop.CompletedAt = now
	s.ActiveEditLoop.Normalize()
	s.EditLoops = append([]EditLoopState{*s.ActiveEditLoop}, s.EditLoops...)
	s.ActiveEditLoop = nil
	s.normalizeEditLoopState()
}

func (s *Session) normalizeEditLoopState() {
	if s == nil {
		return
	}
	if s.ActiveEditLoop != nil {
		s.ActiveEditLoop.Normalize()
		if strings.TrimSpace(s.ActiveEditLoop.ID) == "" {
			s.ActiveEditLoop = nil
		}
	}
	filtered := make([]EditLoopState, 0, len(s.EditLoops))
	for _, loop := range s.EditLoops {
		loop.Normalize()
		if strings.TrimSpace(loop.ID) != "" {
			filtered = append(filtered, loop)
		}
	}
	if len(filtered) > maxSessionEditLoops {
		filtered = filtered[:maxSessionEditLoops]
	}
	s.EditLoops = filtered
}

func (l *EditLoopState) Normalize() {
	if l == nil {
		return
	}
	l.ID = strings.TrimSpace(l.ID)
	l.Goal = strings.Join(strings.Fields(strings.TrimSpace(l.Goal)), " ")
	l.Status = strings.TrimSpace(strings.ToLower(l.Status))
	if l.Status == "" {
		l.Status = editLoopStatusActive
	}
	l.ChangedPaths = normalizeTaskStateList(l.ChangedPaths, 64)
	l.WorkerSummaries = normalizeTaskStateList(l.WorkerSummaries, 12)
	l.VerificationSummary = strings.TrimSpace(l.VerificationSummary)
	l.VerificationStatus = strings.TrimSpace(strings.ToLower(l.VerificationStatus))
	l.VerificationBundleID = strings.TrimSpace(l.VerificationBundleID)
	l.RepairGuidance = strings.TrimSpace(l.RepairGuidance)
	l.RemainingRisks = normalizeTaskStateList(l.RemainingRisks, 12)
	l.FinalReviewVerdict = strings.TrimSpace(strings.ToLower(l.FinalReviewVerdict))
	l.FinalReviewFeedback = strings.TrimSpace(l.FinalReviewFeedback)
	if l.UpdatedAt.IsZero() {
		l.UpdatedAt = l.StartedAt
	}
	l.WorkerEvidence = normalizeEditLoopWorkerEvidence(l.WorkerEvidence, 16)
	l.VerificationEvidence = normalizeEditLoopVerificationEvidence(l.VerificationEvidence, 16)
	l.RetryDecisions = normalizeEditLoopRetryDecisions(l.RetryDecisions, 12)
	if l.OutcomeContract != nil {
		l.OutcomeContract.Normalize()
	}
	l.Events = normalizeEditLoopEvents(l.Events, maxEditLoopEvents)
}

func (l *EditLoopState) RecordEvent(event EditLoopEvent) {
	if l == nil {
		return
	}
	now := time.Now()
	event.Normalize()
	if event.RecordedAt.IsZero() {
		event.RecordedAt = now
	}
	if event.Kind == "" && event.Summary == "" {
		return
	}
	l.Events = append(l.Events, event)
	l.Events = normalizeEditLoopEvents(l.Events, maxEditLoopEvents)
	l.ChangedPaths = normalizeTaskStateList(append(l.ChangedPaths, event.ChangedPaths...), 64)
	switch event.Kind {
	case "apply", "worker_apply":
		if event.Status == "ok" || event.Status == "applied" {
			l.AttemptCount++
			if event.Summary != "" {
				l.WorkerSummaries = appendTaskStateItem(l.WorkerSummaries, event.Summary, 12)
			}
		} else if event.Status == "error" || event.Status == "failed" {
			if event.Summary != "" {
				l.RemainingRisks = appendTaskStateItem(l.RemainingRisks, event.Summary, 12)
			}
		}
		worker := EditLoopWorkerEvidence{
			Source:                  event.Source,
			ToolName:                event.ToolName,
			OwnerNodeID:             event.OwnerNodeID,
			Summary:                 event.Summary,
			Status:                  event.Status,
			ChangedPaths:            event.ChangedPaths,
			LeasePaths:              event.LeasePaths,
			PatchTransactionID:      event.PatchTransactionID,
			PatchTransactionEntryID: event.PatchTransactionEntryID,
			VerificationBundleID:    event.BundleID,
			RecordedAt:              event.RecordedAt,
		}
		l.WorkerEvidence = appendEditLoopWorkerEvidence(l.WorkerEvidence, worker, 16)
	case "verification":
		l.VerificationStatus = strings.TrimSpace(strings.ToLower(event.Status))
		l.VerificationSummary = firstNonBlankString(event.Summary, event.Detail)
		l.VerificationBundleID = firstNonBlankString(event.BundleID, l.VerificationBundleID)
		if event.Status == "failed" {
			if detail := firstNonBlankString(event.Summary, event.Detail); detail != "" {
				l.RemainingRisks = appendTaskStateItem(l.RemainingRisks, "Verification failed: "+detail, 12)
			}
		} else if event.Status == "passed" {
			l.RemainingRisks = removeMatchingTaskStateItem(l.RemainingRisks, "No successful verification")
			if detail := firstNonBlankString(event.Summary, event.Detail); detail != "" {
				l.RemainingRisks = removeExactTaskStateItem(l.RemainingRisks, "Verification failed: "+detail)
				l.RemainingRisks = removeVerificationFailureRiskForPassedSummary(l.RemainingRisks, detail)
			}
		}
		verification := EditLoopVerificationEvidence{
			Source:             event.Source,
			ToolName:           event.ToolName,
			OwnerNodeID:        event.OwnerNodeID,
			Status:             event.Status,
			Summary:            event.Summary,
			Detail:             event.Detail,
			ChangedPaths:       event.ChangedPaths,
			BundleID:           event.BundleID,
			JobIDs:             event.JobIDs,
			LogPaths:           event.LogPaths,
			FailureFingerprint: event.FailureFingerprint,
			RecordedAt:         event.RecordedAt,
		}
		l.VerificationEvidence = appendEditLoopVerificationEvidence(l.VerificationEvidence, verification, 16)
	case "retry":
		l.RetryCount++
		decision := l.buildRetryDecision(event, l.RetryCount)
		l.RetryDecisions = appendEditLoopRetryDecision(l.RetryDecisions, decision, 12)
	case "risk":
		if event.Summary != "" {
			l.RemainingRisks = appendTaskStateItem(l.RemainingRisks, event.Summary, 12)
		}
	case "final_review":
		l.FinalReviewVerdict = strings.TrimSpace(strings.ToLower(event.Status))
		l.FinalReviewFeedback = firstNonBlankString(event.Detail, event.Summary)
	}
	l.UpdatedAt = now
	if l.Status == "" {
		l.Status = editLoopStatusActive
	}
}

func (l *EditLoopState) LinkWorkerVerificationBundle(ownerNodeID string, bundleID string) {
	if l == nil {
		return
	}
	ownerNodeID = strings.TrimSpace(ownerNodeID)
	bundleID = strings.TrimSpace(bundleID)
	if bundleID == "" {
		return
	}
	for index := len(l.WorkerEvidence) - 1; index >= 0; index-- {
		worker := l.WorkerEvidence[index]
		if ownerNodeID != "" && worker.OwnerNodeID != ownerNodeID {
			continue
		}
		if worker.VerificationBundleID == "" {
			l.WorkerEvidence[index].VerificationBundleID = bundleID
			break
		}
	}
	l.VerificationBundleID = firstNonBlankString(l.VerificationBundleID, bundleID)
	l.Normalize()
}

func (l *EditLoopState) UpdateOutcomeContract(reply string, status string) {
	if l == nil {
		return
	}
	contract := &EditLoopOutcomeContract{
		Status:                     firstNonBlankString(status, l.Status),
		ChangedPaths:               append([]string(nil), l.ChangedPaths...),
		WorkerEvidenceCount:        len(l.WorkerEvidence),
		VerificationEvidenceCount:  len(l.VerificationEvidence),
		VerificationStatus:         l.VerificationStatus,
		VerificationSummary:        l.VerificationSummary,
		RetryCount:                 l.RetryCount,
		RemainingRisks:             append([]string(nil), l.RemainingRisks...),
		FinalReviewVerdict:         l.FinalReviewVerdict,
		AnswerMentionsVerification: replyMentionsVerificationOutcome(reply),
		AnswerMentionsRisk:         replyMentionsRemainingRisk(reply) || len(l.RemainingRisks) == 0,
		UpdatedAt:                  time.Now(),
	}
	contract.Normalize()
	l.OutcomeContract = contract
}

func (l EditLoopState) RenderPromptSection() string {
	l.Normalize()
	if strings.TrimSpace(l.ID) == "" {
		return ""
	}
	lines := []string{
		"- Edit loop: " + l.ID + " [" + l.Status + "]",
	}
	if l.Goal != "" {
		lines = append(lines, "- Goal: "+compactPromptSection(l.Goal, 220))
	}
	if len(l.ChangedPaths) > 0 {
		lines = append(lines, "- Changed paths: "+strings.Join(l.ChangedPaths, ", "))
	}
	if len(l.WorkerSummaries) > 0 {
		lines = append(lines, "- Worker/apply summary: "+strings.Join(limitStrings(l.WorkerSummaries, 4), " | "))
	}
	if len(l.WorkerEvidence) > 0 {
		lines = append(lines, fmt.Sprintf("- Worker evidence: %d recorded", len(l.WorkerEvidence)))
		for _, worker := range limitEditLoopWorkerEvidence(l.WorkerEvidence, 3) {
			item := "- worker"
			if worker.Source != "" {
				item += "/" + worker.Source
			}
			if worker.OwnerNodeID != "" {
				item += " node=" + worker.OwnerNodeID
			}
			if worker.Status != "" {
				item += " [" + worker.Status + "]"
			}
			if len(worker.ChangedPaths) > 0 {
				item += " changed=" + strings.Join(worker.ChangedPaths, ", ")
			}
			if worker.Summary != "" {
				item += ": " + compactPromptSection(worker.Summary, 160)
			}
			lines = append(lines, item)
		}
	}
	if l.VerificationStatus != "" || l.VerificationSummary != "" {
		item := "- Verification"
		if l.VerificationStatus != "" {
			item += " [" + l.VerificationStatus + "]"
		}
		if l.VerificationBundleID != "" {
			item += " bundle=" + l.VerificationBundleID
		}
		if l.VerificationSummary != "" {
			item += ": " + compactPromptSection(l.VerificationSummary, 300)
		}
		lines = append(lines, item)
	}
	if len(l.VerificationEvidence) > 0 {
		lines = append(lines, fmt.Sprintf("- Verification evidence: %d recorded", len(l.VerificationEvidence)))
		for _, evidence := range limitEditLoopVerificationEvidence(l.VerificationEvidence, 3) {
			item := "- verify"
			if evidence.Source != "" {
				item += "/" + evidence.Source
			}
			if evidence.BundleID != "" {
				item += " bundle=" + evidence.BundleID
			}
			if evidence.Status != "" {
				item += " [" + evidence.Status + "]"
			}
			if evidence.Summary != "" {
				item += ": " + compactPromptSection(evidence.Summary, 180)
			}
			lines = append(lines, item)
		}
	}
	if l.RetryCount > 0 {
		lines = append(lines, fmt.Sprintf("- Retry count: %d", l.RetryCount))
	}
	if len(l.RetryDecisions) > 0 {
		decision := l.RetryDecisions[len(l.RetryDecisions)-1]
		item := "- Latest retry decision"
		if decision.Action != "" {
			item += " [" + decision.Action + "]"
		}
		if decision.SameFailureCount > 1 {
			item += fmt.Sprintf(" same_failure_count=%d", decision.SameFailureCount)
		}
		if decision.Reason != "" {
			item += ": " + compactPromptSection(decision.Reason, 220)
		}
		lines = append(lines, item)
	}
	if l.RepairGuidance != "" {
		lines = append(lines, "- Repair guidance: "+compactPromptSection(l.RepairGuidance, 260))
	}
	if len(l.RemainingRisks) > 0 {
		lines = append(lines, "- Remaining risk: "+strings.Join(limitStrings(l.RemainingRisks, 4), " | "))
	}
	if l.FinalReviewVerdict != "" {
		item := "- Final review: " + l.FinalReviewVerdict
		if l.FinalReviewFeedback != "" {
			item += " | " + compactPromptSection(l.FinalReviewFeedback, 240)
		}
		lines = append(lines, item)
	}
	if l.OutcomeContract != nil {
		if rendered := strings.TrimSpace(l.OutcomeContract.RenderPromptLine()); rendered != "" {
			lines = append(lines, "- Outcome contract: "+rendered)
		}
	}
	recent := l.Events
	if len(recent) > 5 {
		recent = recent[len(recent)-5:]
	}
	for _, event := range recent {
		line := "- " + event.Kind
		if event.Source != "" {
			line += "/" + event.Source
		}
		if event.Status != "" {
			line += " [" + event.Status + "]"
		}
		if event.Summary != "" {
			line += ": " + compactPromptSection(event.Summary, 180)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (e *EditLoopEvent) Normalize() {
	if e == nil {
		return
	}
	e.Kind = strings.TrimSpace(strings.ToLower(e.Kind))
	e.Source = strings.TrimSpace(strings.ToLower(e.Source))
	e.ToolName = strings.TrimSpace(e.ToolName)
	e.OwnerNodeID = strings.TrimSpace(e.OwnerNodeID)
	e.Summary = strings.TrimSpace(e.Summary)
	e.Detail = strings.TrimSpace(e.Detail)
	e.Status = strings.TrimSpace(strings.ToLower(e.Status))
	e.ChangedPaths = normalizeTaskStateList(e.ChangedPaths, 32)
	e.LeasePaths = normalizeTaskStateList(e.LeasePaths, 32)
	e.PatchTransactionID = strings.TrimSpace(e.PatchTransactionID)
	e.PatchTransactionEntryID = strings.TrimSpace(e.PatchTransactionEntryID)
	e.BundleID = strings.TrimSpace(e.BundleID)
	e.JobIDs = normalizeTaskStateList(e.JobIDs, 32)
	e.LogPaths = normalizeTaskStateList(e.LogPaths, 32)
	e.FailureFingerprint = strings.TrimSpace(e.FailureFingerprint)
	e.RetryAction = strings.TrimSpace(strings.ToLower(e.RetryAction))
}

func normalizeEditLoopEvents(events []EditLoopEvent, limit int) []EditLoopEvent {
	filtered := make([]EditLoopEvent, 0, len(events))
	for _, event := range events {
		event.Normalize()
		if event.Kind == "" && event.Summary == "" {
			continue
		}
		filtered = append(filtered, event)
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	return filtered
}

func (w *EditLoopWorkerEvidence) Normalize() {
	if w == nil {
		return
	}
	w.Source = strings.TrimSpace(strings.ToLower(w.Source))
	w.ToolName = strings.TrimSpace(w.ToolName)
	w.OwnerNodeID = strings.TrimSpace(w.OwnerNodeID)
	w.Summary = strings.TrimSpace(w.Summary)
	w.Status = strings.TrimSpace(strings.ToLower(w.Status))
	w.ChangedPaths = normalizeTaskStateList(w.ChangedPaths, 32)
	w.LeasePaths = normalizeTaskStateList(w.LeasePaths, 32)
	w.PatchTransactionID = strings.TrimSpace(w.PatchTransactionID)
	w.PatchTransactionEntryID = strings.TrimSpace(w.PatchTransactionEntryID)
	w.VerificationBundleID = strings.TrimSpace(w.VerificationBundleID)
}

func normalizeEditLoopWorkerEvidence(items []EditLoopWorkerEvidence, limit int) []EditLoopWorkerEvidence {
	out := make([]EditLoopWorkerEvidence, 0, len(items))
	for _, item := range items {
		item.Normalize()
		if item.Source == "" && item.ToolName == "" && item.Summary == "" && len(item.ChangedPaths) == 0 {
			continue
		}
		out = append(out, item)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func appendEditLoopWorkerEvidence(items []EditLoopWorkerEvidence, item EditLoopWorkerEvidence, limit int) []EditLoopWorkerEvidence {
	item.Normalize()
	if item.Source == "" && item.ToolName == "" && item.Summary == "" && len(item.ChangedPaths) == 0 {
		return normalizeEditLoopWorkerEvidence(items, limit)
	}
	key := editLoopWorkerEvidenceKey(item)
	out := make([]EditLoopWorkerEvidence, 0, len(items)+1)
	replaced := false
	for _, existing := range items {
		existing.Normalize()
		if key != "" && editLoopWorkerEvidenceKey(existing) == key {
			out = append(out, item)
			replaced = true
			continue
		}
		out = append(out, existing)
	}
	if !replaced {
		out = append(out, item)
	}
	return normalizeEditLoopWorkerEvidence(out, limit)
}

func editLoopWorkerEvidenceKey(item EditLoopWorkerEvidence) string {
	if item.PatchTransactionEntryID != "" {
		return "patch-entry:" + item.PatchTransactionEntryID
	}
	return strings.Join([]string{item.Source, item.ToolName, item.OwnerNodeID, strings.Join(item.ChangedPaths, ",")}, "\x00")
}

func limitEditLoopWorkerEvidence(items []EditLoopWorkerEvidence, limit int) []EditLoopWorkerEvidence {
	items = normalizeEditLoopWorkerEvidence(items, 0)
	if limit > 0 && len(items) > limit {
		return items[len(items)-limit:]
	}
	return items
}

func (v *EditLoopVerificationEvidence) Normalize() {
	if v == nil {
		return
	}
	v.Source = strings.TrimSpace(strings.ToLower(v.Source))
	v.ToolName = strings.TrimSpace(v.ToolName)
	v.OwnerNodeID = strings.TrimSpace(v.OwnerNodeID)
	v.Status = strings.TrimSpace(strings.ToLower(v.Status))
	v.Summary = strings.TrimSpace(v.Summary)
	v.Detail = strings.TrimSpace(v.Detail)
	v.ChangedPaths = normalizeTaskStateList(v.ChangedPaths, 32)
	v.BundleID = strings.TrimSpace(v.BundleID)
	v.JobIDs = normalizeTaskStateList(v.JobIDs, 32)
	v.LogPaths = normalizeTaskStateList(v.LogPaths, 32)
	v.FailureFingerprint = strings.TrimSpace(v.FailureFingerprint)
}

func normalizeEditLoopVerificationEvidence(items []EditLoopVerificationEvidence, limit int) []EditLoopVerificationEvidence {
	out := make([]EditLoopVerificationEvidence, 0, len(items))
	for _, item := range items {
		item.Normalize()
		if item.Source == "" && item.ToolName == "" && item.Summary == "" && item.BundleID == "" && len(item.JobIDs) == 0 {
			continue
		}
		out = append(out, item)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func appendEditLoopVerificationEvidence(items []EditLoopVerificationEvidence, item EditLoopVerificationEvidence, limit int) []EditLoopVerificationEvidence {
	item.Normalize()
	if item.Source == "" && item.ToolName == "" && item.Summary == "" && item.BundleID == "" && len(item.JobIDs) == 0 {
		return normalizeEditLoopVerificationEvidence(items, limit)
	}
	key := editLoopVerificationEvidenceKey(item)
	out := make([]EditLoopVerificationEvidence, 0, len(items)+1)
	replaced := false
	for _, existing := range items {
		existing.Normalize()
		if key != "" && editLoopVerificationEvidenceKey(existing) == key {
			out = append(out, item)
			replaced = true
			continue
		}
		out = append(out, existing)
	}
	if !replaced {
		out = append(out, item)
	}
	return normalizeEditLoopVerificationEvidence(out, limit)
}

func editLoopVerificationEvidenceKey(item EditLoopVerificationEvidence) string {
	if item.BundleID != "" {
		return "bundle:" + item.BundleID
	}
	if len(item.JobIDs) > 0 {
		return "jobs:" + strings.Join(item.JobIDs, ",")
	}
	return strings.Join([]string{item.Source, item.ToolName, item.OwnerNodeID, item.Status, item.Summary}, "\x00")
}

func limitEditLoopVerificationEvidence(items []EditLoopVerificationEvidence, limit int) []EditLoopVerificationEvidence {
	items = normalizeEditLoopVerificationEvidence(items, 0)
	if limit > 0 && len(items) > limit {
		return items[len(items)-limit:]
	}
	return items
}

func (d *EditLoopRetryDecision) Normalize() {
	if d == nil {
		return
	}
	d.Action = strings.TrimSpace(strings.ToLower(d.Action))
	d.Reason = strings.TrimSpace(d.Reason)
	d.FailureFingerprint = strings.TrimSpace(d.FailureFingerprint)
	if d.SameFailureCount < 0 {
		d.SameFailureCount = 0
	}
}

func normalizeEditLoopRetryDecisions(items []EditLoopRetryDecision, limit int) []EditLoopRetryDecision {
	out := make([]EditLoopRetryDecision, 0, len(items))
	for _, item := range items {
		item.Normalize()
		if item.Action == "" && item.Reason == "" && item.FailureFingerprint == "" {
			continue
		}
		out = append(out, item)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func appendEditLoopRetryDecision(items []EditLoopRetryDecision, item EditLoopRetryDecision, limit int) []EditLoopRetryDecision {
	item.Normalize()
	if item.Action == "" && item.Reason == "" && item.FailureFingerprint == "" {
		return normalizeEditLoopRetryDecisions(items, limit)
	}
	out := append(items, item)
	return normalizeEditLoopRetryDecisions(out, limit)
}

func (l *EditLoopState) buildRetryDecision(event EditLoopEvent, attempt int) EditLoopRetryDecision {
	fingerprint := firstNonBlankString(event.FailureFingerprint, editLoopFailureFingerprint(event.Detail), editLoopFailureFingerprint(event.Summary))
	sameFailureCount := 1
	for _, decision := range l.RetryDecisions {
		if fingerprint != "" && strings.EqualFold(decision.FailureFingerprint, fingerprint) {
			sameFailureCount = max(sameFailureCount, decision.SameFailureCount+1)
		}
	}
	action := strings.TrimSpace(strings.ToLower(event.RetryAction))
	if action == "" {
		switch {
		case sameFailureCount >= 3:
			action = "escalate_reviewer"
		case sameFailureCount >= 2:
			action = "change_strategy"
		default:
			action = "repair_and_retry"
		}
	}
	reason := strings.TrimSpace(event.Summary)
	if action == "change_strategy" && reason != "" && !strings.Contains(strings.ToLower(reason), "strategy") {
		reason += " Use a materially different repair or narrower verification path."
	}
	if action == "escalate_reviewer" && reason != "" && !strings.Contains(strings.ToLower(reason), "reviewer") {
		reason += " Ask reviewer guidance before another retry."
	}
	return EditLoopRetryDecision{
		Attempt:            attempt,
		Action:             action,
		Reason:             reason,
		FailureFingerprint: fingerprint,
		SameFailureCount:   sameFailureCount,
		RecordedAt:         event.RecordedAt,
	}
}

func editLoopFailureFingerprint(text string) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(lower, "\r\n", "\n"), "\n")
	selected := make([]string, 0, 3)
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		selected = append(selected, compactPromptSection(line, 140))
		if len(selected) >= 3 {
			break
		}
	}
	return strings.Join(selected, " | ")
}

func (c *EditLoopOutcomeContract) Normalize() {
	if c == nil {
		return
	}
	c.Status = strings.TrimSpace(strings.ToLower(c.Status))
	c.ChangedPaths = normalizeTaskStateList(c.ChangedPaths, 64)
	c.VerificationStatus = strings.TrimSpace(strings.ToLower(c.VerificationStatus))
	c.VerificationSummary = strings.TrimSpace(c.VerificationSummary)
	c.RemainingRisks = normalizeTaskStateList(c.RemainingRisks, 12)
	c.FinalReviewVerdict = strings.TrimSpace(strings.ToLower(c.FinalReviewVerdict))
}

func (c EditLoopOutcomeContract) RenderPromptLine() string {
	c.Normalize()
	parts := make([]string, 0, 8)
	if c.Status != "" {
		parts = append(parts, "status="+c.Status)
	}
	if len(c.ChangedPaths) > 0 {
		parts = append(parts, fmt.Sprintf("changed=%d", len(c.ChangedPaths)))
	}
	if c.WorkerEvidenceCount > 0 {
		parts = append(parts, fmt.Sprintf("worker_evidence=%d", c.WorkerEvidenceCount))
	}
	if c.VerificationEvidenceCount > 0 {
		parts = append(parts, fmt.Sprintf("verification_evidence=%d", c.VerificationEvidenceCount))
	}
	if c.VerificationStatus != "" {
		parts = append(parts, "verification="+c.VerificationStatus)
	}
	if c.RetryCount > 0 {
		parts = append(parts, fmt.Sprintf("retries=%d", c.RetryCount))
	}
	if len(c.RemainingRisks) > 0 {
		parts = append(parts, fmt.Sprintf("remaining_risk=%d", len(c.RemainingRisks)))
	}
	if c.FinalReviewVerdict != "" {
		parts = append(parts, "final_review="+c.FinalReviewVerdict)
	}
	return strings.Join(parts, " ")
}

func renderEditLoopOutcomeContractPrompt(loop *EditLoopState) string {
	if loop == nil {
		return ""
	}
	loop.Normalize()
	if strings.TrimSpace(loop.ID) == "" {
		return ""
	}
	lines := []string{
		"- changed_paths: " + editLoopContractValue(strings.Join(loop.ChangedPaths, ", ")),
		fmt.Sprintf("- worker_evidence_count: %d", len(loop.WorkerEvidence)),
		"- verification_status: " + editLoopContractValue(loop.VerificationStatus),
		"- verification_summary: " + editLoopContractValue(compactPromptSection(loop.VerificationSummary, 180)),
		fmt.Sprintf("- retry_count: %d", loop.RetryCount),
		"- remaining_risk: " + editLoopContractValue(strings.Join(loop.RemainingRisks, " | ")),
		"- final_review: " + editLoopContractValue(loop.FinalReviewVerdict),
	}
	if len(loop.RetryDecisions) > 0 {
		decision := loop.RetryDecisions[len(loop.RetryDecisions)-1]
		lines = append(lines, "- retry_decision: "+editLoopContractValue(decision.Action))
	}
	return strings.Join(lines, "\n")
}

func editLoopContractValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "none"
	}
	return value
}

func editLoopClosedStatus(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case editLoopStatusCompleted, editLoopStatusRiskAccepted:
		return true
	default:
		return false
	}
}

func latestEditLoop(sess *Session) *EditLoopState {
	if sess == nil {
		return nil
	}
	if sess.ActiveEditLoop != nil {
		return sess.ActiveEditLoop
	}
	if len(sess.EditLoops) > 0 {
		return &sess.EditLoops[0]
	}
	return nil
}

func currentTurnActiveEditLoop(sess *Session) *EditLoopState {
	if sess == nil || sess.ActiveEditLoop == nil {
		return nil
	}
	if !activeEditLoopMatchesCurrentTurn(sess, sess.ActiveEditLoop) {
		return nil
	}
	return sess.ActiveEditLoop
}

func promptActiveEditLoop(sess *Session) *EditLoopState {
	if current := currentTurnActiveEditLoop(sess); current != nil {
		return current
	}
	if sess == nil || sess.ActiveEditLoop == nil {
		return nil
	}
	latestUser := strings.TrimSpace(baseUserQueryText(latestExternalOrUserMessageText(sess.Messages)))
	switch classifyTurnIntent(latestUser) {
	case TurnIntentContinueLastTask, TurnIntentExplainCurrentState:
		return sess.ActiveEditLoop
	default:
		return nil
	}
}

func activeEditLoopMatchesCurrentTurn(sess *Session, loop *EditLoopState) bool {
	if sess == nil || loop == nil {
		return false
	}
	goal := normalizedPatchTransactionGoal(loop.Goal)
	if goal == "" {
		return true
	}
	latestUser := strings.TrimSpace(baseUserQueryText(latestExternalOrUserMessageText(sess.Messages)))
	if latestUser != "" && !looksLikeInternalReviewFeedbackUserMessage(latestUser) {
		if normalizedPatchTransactionGoal(latestUser) == goal {
			return true
		}
		return classifyTurnIntent(latestUser) == TurnIntentContinueLastTask
	}
	if sess.AcceptanceContract != nil {
		if normalizedPatchTransactionGoal(sess.AcceptanceContract.SourcePrompt) == goal {
			return true
		}
	}
	if sess.TaskState != nil {
		if normalizedPatchTransactionGoal(sess.TaskState.Goal) == goal {
			return true
		}
	}
	return false
}

func editLoopGoal(sess *Session) string {
	if sess == nil {
		return ""
	}
	if sess.TaskState != nil && strings.TrimSpace(sess.TaskState.Goal) != "" {
		return sess.TaskState.Goal
	}
	return latestExternalOrUserMessageText(sess.Messages)
}
