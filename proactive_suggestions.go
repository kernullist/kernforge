package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	SuggestionModeObserve = "observe"
	SuggestionModeSuggest = "suggest"
	SuggestionModeConfirm = "confirm"

	SuggestionStatusShown     = "shown"
	SuggestionStatusAccepted  = "accepted"
	SuggestionStatusDismissed = "dismissed"
	SuggestionStatusExecuted  = "executed"
)

type SituationSnapshot struct {
	SchemaVersion        int                 `json:"schema_version"`
	CreatedAt            time.Time           `json:"created_at"`
	CurrentGoal          string              `json:"current_goal,omitempty"`
	WorkflowPhase        string              `json:"workflow_phase,omitempty"`
	BlockingIssue        string              `json:"blocking_issue,omitempty"`
	RiskLevel            string              `json:"risk_level,omitempty"`
	Confidence           string              `json:"confidence,omitempty"`
	OpenArtifacts        []string            `json:"open_artifacts,omitempty"`
	MissingEvidence      []string            `json:"missing_evidence,omitempty"`
	MissingVerification  []string            `json:"missing_verification,omitempty"`
	ChangedPaths         []string            `json:"changed_paths,omitempty"`
	StaleDocs            []string            `json:"stale_docs,omitempty"`
	FuzzGaps             []string            `json:"fuzz_gaps,omitempty"`
	RecentEvents         []ConversationEvent `json:"recent_events,omitempty"`
	SuggestionCandidates []Suggestion        `json:"suggestion_candidates,omitempty"`
}

type Suggestion struct {
	ID                   string    `json:"id"`
	Type                 string    `json:"type"`
	Title                string    `json:"title"`
	Reason               string    `json:"reason"`
	EvidenceRefs         []string  `json:"evidence_refs,omitempty"`
	Command              string    `json:"command,omitempty"`
	EstimatedCost        string    `json:"estimated_cost,omitempty"`
	Risk                 string    `json:"risk,omitempty"`
	RequiresConfirmation bool      `json:"requires_confirmation,omitempty"`
	DedupKey             string    `json:"dedup_key"`
	ExpiresAtEventID     string    `json:"expires_at_event_id,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
}

type SuggestionRecord struct {
	Suggestion Suggestion `json:"suggestion"`
	Status     string     `json:"status"`
	ShownAt    time.Time  `json:"shown_at,omitempty"`
	UpdatedAt  time.Time  `json:"updated_at,omitempty"`
	Result     string     `json:"result,omitempty"`
}

type SuggestionMemory struct {
	SchemaVersion int                  `json:"schema_version"`
	Mode          string               `json:"mode,omitempty"`
	Records       []SuggestionRecord   `json:"records,omitempty"`
	Cooldowns     map[string]time.Time `json:"cooldowns,omitempty"`
}

type ProactiveSources struct {
	Workspace     Workspace
	Session       *Session
	Evidence      *EvidenceStore
	VerifyHistory *VerificationHistoryStore
	FunctionFuzz  *FunctionFuzzStore
	FuzzCampaigns *FuzzCampaignStore
}

func (m *SuggestionMemory) ApproxChars() int {
	if m == nil {
		return 0
	}
	total := len(m.Mode)
	for _, record := range m.Records {
		total += len(record.Status) + len(record.Result)
		total += len(record.Suggestion.ID) + len(record.Suggestion.Type)
		total += len(record.Suggestion.Title) + len(record.Suggestion.Reason)
		total += len(record.Suggestion.Command) + len(record.Suggestion.DedupKey)
		for _, ref := range record.Suggestion.EvidenceRefs {
			total += len(ref)
		}
	}
	for key := range m.Cooldowns {
		total += len(key)
	}
	return total
}

func (s *Session) normalizeSuggestionMemory() {
	if s == nil {
		return
	}
	if s.SuggestionMemory == nil {
		return
	}
	if s.SuggestionMemory.SchemaVersion == 0 {
		s.SuggestionMemory.SchemaVersion = 1
	}
	mode := strings.ToLower(strings.TrimSpace(s.SuggestionMemory.Mode))
	switch mode {
	case "", SuggestionModeObserve, SuggestionModeSuggest, SuggestionModeConfirm:
		s.SuggestionMemory.Mode = mode
	default:
		s.SuggestionMemory.Mode = SuggestionModeSuggest
	}
	if s.SuggestionMemory.Cooldowns == nil {
		s.SuggestionMemory.Cooldowns = map[string]time.Time{}
	}
	records := make([]SuggestionRecord, 0, len(s.SuggestionMemory.Records))
	for _, record := range s.SuggestionMemory.Records {
		record.Suggestion = normalizeSuggestion(record.Suggestion)
		if strings.TrimSpace(record.Suggestion.DedupKey) == "" {
			continue
		}
		if strings.TrimSpace(record.Status) == "" {
			record.Status = SuggestionStatusShown
		}
		records = append(records, record)
	}
	if len(records) > 80 {
		records = append([]SuggestionRecord(nil), records[len(records)-80:]...)
	}
	s.SuggestionMemory.Records = records
}

func (s *Session) ensureSuggestionMemory() *SuggestionMemory {
	if s == nil {
		return nil
	}
	if s.SuggestionMemory == nil {
		s.SuggestionMemory = &SuggestionMemory{
			SchemaVersion: 1,
			Mode:          SuggestionModeSuggest,
			Cooldowns:     map[string]time.Time{},
		}
	}
	s.normalizeSuggestionMemory()
	return s.SuggestionMemory
}

func (m *SuggestionMemory) modeOrDefault() string {
	if m == nil {
		return SuggestionModeSuggest
	}
	mode := strings.ToLower(strings.TrimSpace(m.Mode))
	if mode == "" {
		return SuggestionModeSuggest
	}
	return mode
}

func (a *Agent) proactiveSources() ProactiveSources {
	if a == nil {
		return ProactiveSources{}
	}
	return ProactiveSources{
		Workspace:     a.Workspace,
		Session:       a.Session,
		Evidence:      a.Evidence,
		VerifyHistory: a.VerifyHistory,
		FunctionFuzz:  a.FunctionFuzz,
		FuzzCampaigns: a.FuzzCampaigns,
	}
}

func (rt *runtimeState) proactiveSources() ProactiveSources {
	if rt == nil {
		return ProactiveSources{}
	}
	return ProactiveSources{
		Workspace:     rt.workspace,
		Session:       rt.session,
		Evidence:      rt.evidence,
		VerifyHistory: rt.verifyHistory,
		FunctionFuzz:  rt.functionFuzz,
		FuzzCampaigns: rt.fuzzCampaigns,
	}
}

func BuildSituationSnapshot(src ProactiveSources) SituationSnapshot {
	sess := src.Session
	if sess != nil {
		sess.RefreshConversationState()
	}
	snapshot := SituationSnapshot{
		SchemaVersion: 1,
		CreatedAt:     time.Now(),
		RiskLevel:     "low",
		Confidence:    "medium",
	}
	if sess != nil && sess.ConversationState != nil {
		state := sess.ConversationState
		snapshot.CurrentGoal = strings.TrimSpace(state.LastUserGoal)
		snapshot.WorkflowPhase = firstNonBlankString(state.CurrentWorkflow, state.LastCommand)
		snapshot.BlockingIssue = strings.TrimSpace(state.LastError)
		snapshot.OpenArtifacts = append(snapshot.OpenArtifacts, state.OpenArtifacts...)
	}
	if sess != nil {
		snapshot.RecentEvents = recentNonUserConversationEvents(sess, 8)
	}
	root := firstNonBlankString(src.Workspace.Root, src.Workspace.BaseRoot)
	if strings.TrimSpace(root) != "" {
		snapshot.ChangedPaths = gitChangedPaths(root)
		snapshot.StaleDocs = latestAnalysisStaleDocs(root)
	}
	if len(snapshot.ChangedPaths) > 0 {
		snapshot.RiskLevel = riskLevelForChangedPaths(snapshot.ChangedPaths)
		if !recentVerificationCoversChanges(sess, src.VerifyHistory, root, snapshot.ChangedPaths) {
			snapshot.MissingVerification = append(snapshot.MissingVerification, verificationGapForChangedPaths(snapshot.ChangedPaths))
		}
	}
	if len(snapshot.StaleDocs) > 0 {
		snapshot.MissingEvidence = append(snapshot.MissingEvidence, "latest analysis docs contain stale markers")
	}
	snapshot.FuzzGaps = fuzzSuggestionGaps(src)
	snapshot.SuggestionCandidates = BuildProactiveSuggestions(snapshot, src)
	return snapshot
}

func BuildProactiveSuggestions(snapshot SituationSnapshot, src ProactiveSources) []Suggestion {
	now := time.Now()
	out := []Suggestion{}
	add := func(item Suggestion) {
		item = normalizeSuggestion(item)
		if item.CreatedAt.IsZero() {
			item.CreatedAt = now
		}
		if item.ID == "" {
			item.ID = "sg-" + shortStableID(item.DedupKey)
		}
		out = append(out, item)
	}
	if event, ok := latestProviderRateLimitEvent(src.Session); ok {
		model := firstNonBlankString(event.Entities["model"], event.Entities["session_model"])
		shard := event.Entities["shard"]
		reason := "최근 provider rate-limit/timeout event가 현재 workflow를 막았습니다."
		if model != "" {
			reason += " model=" + model
		}
		if shard != "" {
			reason += " shard=" + shard
		}
		add(Suggestion{
			Type:                 "retry_or_switch_model",
			Title:                "실패한 model request를 재시도하거나 fallback model로 전환",
			Reason:               reason,
			EvidenceRefs:         []string{event.ID},
			Command:              "/model",
			EstimatedCost:        "low",
			Risk:                 "low",
			RequiresConfirmation: true,
			DedupKey:             "provider:" + firstNonBlankString(event.Entities["code"], event.Entities["category"], event.ID) + ":" + model + ":" + shard,
			ExpiresAtEventID:     event.ID,
		})
	}
	if len(snapshot.MissingVerification) > 0 {
		add(Suggestion{
			Type:                 "run_verification",
			Title:                "변경 파일에 맞는 verification 실행",
			Reason:               compactPromptSection(strings.Join(snapshot.MissingVerification, "; "), 220),
			EvidenceRefs:         append([]string{}, snapshot.ChangedPaths...),
			Command:              "/verify",
			EstimatedCost:        "medium",
			Risk:                 snapshot.RiskLevel,
			RequiresConfirmation: false,
			DedupKey:             "verify:" + strings.Join(limitStrings(snapshot.ChangedPaths, 8), "|"),
		})
		if !sessionHasActiveAutomation(src.Session, AutomationTypeRecurringVerification) {
			add(Suggestion{
				Type:                 AutomationTypeRecurringVerification,
				Title:                "verification 반복 점검 automation 등록",
				Reason:               "변경 파일 verification이 필요한 상태라서 /automation run 으로 반복 실행 가능한 점검 슬롯을 먼저 등록합니다.",
				EvidenceRefs:         append([]string{}, snapshot.ChangedPaths...),
				Command:              "/automation add recurring-verification /verify",
				EstimatedCost:        "low",
				Risk:                 "low",
				RequiresConfirmation: false,
				DedupKey:             "automation:recurring-verification:" + strings.Join(limitStrings(snapshot.ChangedPaths, 8), "|"),
			})
		}
	}
	if len(snapshot.StaleDocs) > 0 {
		add(Suggestion{
			Type:                 "refresh_analysis",
			Title:                "stale analysis docs refresh",
			Reason:               "latest analysis docs에 stale marker가 남아 있습니다: " + strings.Join(limitStrings(snapshot.StaleDocs, 3), "; "),
			EvidenceRefs:         append([]string{}, snapshot.StaleDocs...),
			Command:              "/docs-refresh",
			EstimatedCost:        "medium",
			Risk:                 "low",
			RequiresConfirmation: false,
			DedupKey:             "docs-refresh:" + strings.Join(limitStrings(snapshot.StaleDocs, 6), "|"),
		})
	}
	if failed := latestFailedVerification(src.Session); failed != "" {
		add(Suggestion{
			Type:                 "inspect_failure",
			Title:                "반복 실행보다 실패 원인 drilldown",
			Reason:               compactPromptSection(failed, 220),
			Command:              "/verify",
			EstimatedCost:        "medium",
			Risk:                 "medium",
			RequiresConfirmation: false,
			DedupKey:             "verify-failure:" + failed,
		})
	}
	if next := pendingWorkflowSuggestion(src.Session); next != "" {
		add(Suggestion{
			Type:                 "continue_workflow",
			Title:                "직전 handoff의 다음 단계 진행",
			Reason:               compactPromptSection(next, 220),
			Command:              extractCommandCandidate(next),
			EstimatedCost:        "low",
			Risk:                 "low",
			RequiresConfirmation: false,
			DedupKey:             "continue:" + next,
		})
	}
	for _, gap := range snapshot.FuzzGaps {
		command := "/fuzz-campaign run"
		if strings.Contains(strings.ToLower(gap), "minimiz") {
			command = ""
		}
		add(Suggestion{
			Type:                 "fuzz_next_step",
			Title:                "fuzz campaign gap 처리",
			Reason:               compactPromptSection(gap, 220),
			Command:              command,
			EstimatedCost:        "medium",
			Risk:                 "medium",
			RequiresConfirmation: true,
			DedupKey:             "fuzz:" + gap,
		})
	}
	if shouldSuggestCheckpoint(snapshot) {
		add(Suggestion{
			Type:                 "checkpoint_or_worktree",
			Title:                "위험도 높은 변경 전 checkpoint 생성",
			Reason:               "kernel/anti-cheat/telemetry/memory 관련 dirty change가 있어 rollback point를 먼저 남기는 편이 안전합니다.",
			EvidenceRefs:         append([]string{}, snapshot.ChangedPaths...),
			Command:              "/checkpoint",
			EstimatedCost:        "low",
			Risk:                 "low",
			RequiresConfirmation: false,
			DedupKey:             "checkpoint:" + strings.Join(limitStrings(snapshot.ChangedPaths, 8), "|"),
		})
	}
	if len(snapshot.ChangedPaths) > 0 && !sessionHasActiveAutomation(src.Session, AutomationTypePRReview) {
		add(Suggestion{
			Type:                 AutomationTypePRReview,
			Title:                "PR review automation report 준비",
			Reason:               "현재 변경 파일을 기준으로 status/diff/stat/checklist를 남겨 PR 직전 검토 루프를 이어갈 수 있게 합니다.",
			EvidenceRefs:         append([]string{}, snapshot.ChangedPaths...),
			Command:              "/automation add pr-review /review-pr",
			EstimatedCost:        "low",
			Risk:                 "low",
			RequiresConfirmation: false,
			DedupKey:             "automation:pr-review:" + strings.Join(limitStrings(snapshot.ChangedPaths, 8), "|"),
		})
	}
	if gap := evidenceCaptureGap(src); gap != "" {
		add(Suggestion{
			Type:                 "evidence_capture",
			Title:                "결과를 evidence로 캡처",
			Reason:               gap,
			Command:              "/evidence",
			EstimatedCost:        "low",
			Risk:                 "low",
			RequiresConfirmation: false,
			DedupKey:             "evidence:" + gap,
		})
	}
	if cleanup := cleanupFeatureSuggestion(src.Session); cleanup != "" {
		add(Suggestion{
			Type:                 "cleanup_or_close_feature",
			Title:                "완료된 feature 정리",
			Reason:               cleanup,
			Command:              "/new-feature close",
			EstimatedCost:        "low",
			Risk:                 "low",
			RequiresConfirmation: false,
			DedupKey:             "feature-close:" + cleanup,
		})
	}
	return rankSuggestions(uniqueSuggestions(out))
}

func (a *Agent) maybeAppendProactiveSuggestion(reply string, userText string) string {
	if a == nil || a.Session == nil {
		return reply
	}
	mem := a.Session.ensureSuggestionMemory()
	if mem == nil || mem.modeOrDefault() == SuggestionModeObserve {
		return reply
	}
	snapshot := BuildSituationSnapshot(a.proactiveSources())
	suggestion, ok := NextActionSuggestion(snapshot, mem, classifyTurnIntent(userText))
	if !ok {
		return reply
	}
	mem.recordShown(suggestion)
	a.Session.SuggestionMemory = mem
	line := renderSuggestionInline(suggestion, mem.modeOrDefault())
	if strings.TrimSpace(line) == "" || strings.Contains(reply, line) {
		return reply
	}
	return strings.TrimSpace(reply) + "\n\n" + line
}

func NextActionSuggestion(snapshot SituationSnapshot, mem *SuggestionMemory, intent TurnIntent) (Suggestion, bool) {
	candidates := rankSuggestions(snapshot.SuggestionCandidates)
	if len(candidates) == 0 {
		return Suggestion{}, false
	}
	limit := 1
	_ = limit
	for _, item := range candidates {
		if !suggestionAllowed(item, mem) {
			continue
		}
		if item.Type != "retry_or_switch_model" {
			continue
		}
		if intent == TurnIntentAskProjectKnowledge && item.Risk == "low" && item.Type != "retry_or_switch_model" {
			continue
		}
		return item, true
	}
	return Suggestion{}, false
}

func (m *SuggestionMemory) recordShown(suggestion Suggestion) {
	if m == nil {
		return
	}
	suggestion = normalizeSuggestion(suggestion)
	now := time.Now()
	for i := range m.Records {
		if strings.EqualFold(m.Records[i].Suggestion.DedupKey, suggestion.DedupKey) {
			m.Records[i].Suggestion = suggestion
			m.Records[i].Status = SuggestionStatusShown
			if m.Records[i].ShownAt.IsZero() {
				m.Records[i].ShownAt = now
			}
			m.Records[i].UpdatedAt = now
			return
		}
	}
	m.Records = append(m.Records, SuggestionRecord{
		Suggestion: suggestion,
		Status:     SuggestionStatusShown,
		ShownAt:    now,
		UpdatedAt:  now,
	})
}

func (m *SuggestionMemory) mark(idOrKey string, status string, result string) (SuggestionRecord, bool) {
	if m == nil {
		return SuggestionRecord{}, false
	}
	query := strings.ToLower(strings.TrimSpace(idOrKey))
	for i := range m.Records {
		record := &m.Records[i]
		if strings.ToLower(record.Suggestion.ID) == query || strings.ToLower(record.Suggestion.DedupKey) == query {
			record.Status = status
			record.Result = strings.TrimSpace(result)
			record.UpdatedAt = time.Now()
			if status == SuggestionStatusDismissed {
				if m.Cooldowns == nil {
					m.Cooldowns = map[string]time.Time{}
				}
				m.Cooldowns[record.Suggestion.DedupKey] = time.Now().Add(4 * time.Hour)
			}
			return *record, true
		}
	}
	return SuggestionRecord{}, false
}

func suggestionAllowed(item Suggestion, mem *SuggestionMemory) bool {
	if strings.TrimSpace(item.DedupKey) == "" {
		return false
	}
	if mem == nil {
		return true
	}
	if until, ok := mem.Cooldowns[item.DedupKey]; ok && time.Now().Before(until) {
		return false
	}
	for _, record := range mem.Records {
		if !strings.EqualFold(record.Suggestion.DedupKey, item.DedupKey) {
			continue
		}
		switch record.Status {
		case SuggestionStatusDismissed, SuggestionStatusAccepted, SuggestionStatusExecuted:
			return false
		case SuggestionStatusShown:
			if !record.ShownAt.IsZero() && time.Since(record.ShownAt) < 30*time.Minute {
				return false
			}
		}
	}
	return true
}

func renderSuggestionInline(suggestion Suggestion, mode string) string {
	prefix := "Next suggested step"
	if mode == SuggestionModeConfirm || suggestion.RequiresConfirmation {
		prefix = "Suggested next step"
	}
	command := strings.TrimSpace(suggestion.Command)
	if command != "" {
		return fmt.Sprintf("%s: %s (`%s`). Reason: %s", prefix, suggestion.Title, command, suggestion.Reason)
	}
	return fmt.Sprintf("%s: %s. Reason: %s", prefix, suggestion.Title, suggestion.Reason)
}

func renderSuggestionList(snapshot SituationSnapshot, mem *SuggestionMemory, limit int) string {
	items := rankSuggestions(snapshot.SuggestionCandidates)
	if limit <= 0 {
		limit = 8
	}
	lines := []string{}
	for _, item := range items {
		status := suggestionMemoryStatus(item, mem)
		command := ""
		if strings.TrimSpace(item.Command) != "" {
			command = " | command=" + item.Command
		}
		lines = append(lines, fmt.Sprintf("- %s [%s] %s | risk=%s cost=%s%s\n  reason: %s", item.ID, status, item.Title, valueOrDefault(item.Risk, "unknown"), valueOrDefault(item.EstimatedCost, "unknown"), command, item.Reason))
		if len(lines) >= limit {
			break
		}
	}
	if len(lines) == 0 {
		return "No proactive suggestions for the current situation."
	}
	return strings.Join(lines, "\n")
}

func renderSituationSnapshot(snapshot SituationSnapshot) string {
	lines := []string{
		"Current situation:",
		"- goal: " + valueOrDefault(snapshot.CurrentGoal, "unset"),
		"- phase: " + valueOrDefault(snapshot.WorkflowPhase, "idle"),
		"- risk: " + valueOrDefault(snapshot.RiskLevel, "unknown"),
		"- confidence: " + valueOrDefault(snapshot.Confidence, "unknown"),
	}
	if strings.TrimSpace(snapshot.BlockingIssue) != "" {
		lines = append(lines, "- blocking issue: "+compactPromptSection(snapshot.BlockingIssue, 220))
	}
	if len(snapshot.ChangedPaths) > 0 {
		lines = append(lines, "- changed paths: "+strings.Join(limitStrings(snapshot.ChangedPaths, 8), ", "))
	}
	if len(snapshot.MissingVerification) > 0 {
		lines = append(lines, "- missing verification: "+strings.Join(snapshot.MissingVerification, "; "))
	}
	if len(snapshot.StaleDocs) > 0 {
		lines = append(lines, "- stale docs: "+strings.Join(limitStrings(snapshot.StaleDocs, 5), "; "))
	}
	if len(snapshot.FuzzGaps) > 0 {
		lines = append(lines, "- fuzz gaps: "+strings.Join(limitStrings(snapshot.FuzzGaps, 5), "; "))
	}
	return strings.Join(lines, "\n")
}

func suggestionMemoryStatus(item Suggestion, mem *SuggestionMemory) string {
	if mem == nil {
		return "new"
	}
	for _, record := range mem.Records {
		if strings.EqualFold(record.Suggestion.DedupKey, item.DedupKey) {
			return valueOrDefault(record.Status, "shown")
		}
	}
	return "new"
}

func (rt *runtimeState) handleSuggestCommand(args string) error {
	if rt == nil || rt.session == nil {
		return fmt.Errorf("no active session")
	}
	mem := rt.session.ensureSuggestionMemory()
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) == 0 || strings.EqualFold(fields[0], "status") || strings.EqualFold(fields[0], "list") {
		snapshot := BuildSituationSnapshot(rt.proactiveSources())
		rt.syncSuggestionCandidatesToTaskGraph(snapshot.SuggestionCandidates, mem)
		fmt.Fprintln(rt.writer, rt.ui.section("Situation"))
		fmt.Fprintln(rt.writer, renderSituationSnapshot(snapshot))
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, rt.ui.section("Suggestions"))
		fmt.Fprintln(rt.writer, renderSuggestionList(snapshot, mem, 8))
		return rt.store.Save(rt.session)
	}
	switch strings.ToLower(fields[0]) {
	case "mode":
		if len(fields) < 2 {
			fmt.Fprintln(rt.writer, rt.ui.infoLine("Suggestion mode: "+mem.modeOrDefault()))
			return nil
		}
		mode := strings.ToLower(strings.TrimSpace(fields[1]))
		switch mode {
		case SuggestionModeObserve, SuggestionModeSuggest, SuggestionModeConfirm:
			mem.Mode = mode
			fmt.Fprintln(rt.writer, rt.ui.successLine("Suggestion mode set to "+mode))
			return rt.store.Save(rt.session)
		default:
			return fmt.Errorf("usage: /suggest mode <observe|suggest|confirm>")
		}
	case "accept":
		if len(fields) < 2 {
			return fmt.Errorf("usage: /suggest accept <id>")
		}
		record, ok := mem.mark(fields[1], SuggestionStatusAccepted, "accepted by user")
		if !ok {
			return fmt.Errorf("suggestion not found: %s", fields[1])
		}
		rt.syncSuggestionToTaskGraph(record)
		if err := rt.promoteSuggestionPreferenceToMemory(record, SuggestionStatusAccepted); err != nil {
			fmt.Fprintln(rt.writer, rt.ui.warnLine("Could not promote suggestion preference to memory: "+err.Error()))
		}
		rt.session.AppendConversationEvent(ConversationEvent{
			Kind:     conversationEventKindHandoff,
			Severity: conversationSeverityInfo,
			Summary:  "suggestion accepted: " + record.Suggestion.Title,
			Entities: map[string]string{
				"suggestion_id":   record.Suggestion.ID,
				"suggestion_type": record.Suggestion.Type,
				"command":         record.Suggestion.Command,
			},
		})
		fmt.Fprintln(rt.writer, rt.ui.successLine("Accepted suggestion: "+record.Suggestion.ID))
		if strings.TrimSpace(record.Suggestion.Command) != "" {
			fmt.Fprintln(rt.writer, rt.ui.hintLine("Suggested command: "+record.Suggestion.Command))
		}
		if mem.modeOrDefault() == SuggestionModeConfirm && strings.TrimSpace(record.Suggestion.Command) != "" {
			result, err := rt.executeSafeSuggestionCommand(record.Suggestion.Command)
			if err != nil {
				fmt.Fprintln(rt.writer, rt.ui.warnLine("Accepted, but automatic execution was skipped: "+err.Error()))
			} else {
				if updated, ok := mem.mark(record.Suggestion.ID, SuggestionStatusExecuted, result); ok {
					record = updated
					rt.syncSuggestionToTaskGraph(record)
				}
				rt.session.AppendConversationEvent(ConversationEvent{
					Kind:     conversationEventKindToolResult,
					Severity: conversationSeverityInfo,
					Summary:  "suggestion executed: " + record.Suggestion.Title,
					Entities: map[string]string{
						"suggestion_id":   record.Suggestion.ID,
						"suggestion_type": record.Suggestion.Type,
						"command":         record.Suggestion.Command,
						"result":          result,
					},
				})
				fmt.Fprintln(rt.writer, rt.ui.successLine("Executed accepted suggestion: "+record.Suggestion.Command))
			}
		}
		return rt.store.Save(rt.session)
	case "dismiss":
		if len(fields) < 2 {
			return fmt.Errorf("usage: /suggest dismiss <id>")
		}
		record, ok := mem.mark(fields[1], SuggestionStatusDismissed, "dismissed by user")
		if !ok {
			return fmt.Errorf("suggestion not found: %s", fields[1])
		}
		rt.syncSuggestionToTaskGraph(record)
		if err := rt.promoteSuggestionPreferenceToMemory(record, SuggestionStatusDismissed); err != nil {
			fmt.Fprintln(rt.writer, rt.ui.warnLine("Could not promote suggestion preference to memory: "+err.Error()))
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Dismissed suggestion: "+record.Suggestion.ID))
		return rt.store.Save(rt.session)
	default:
		return fmt.Errorf("usage: /suggest [status|list|accept <id>|dismiss <id>|mode <observe|suggest|confirm>]")
	}
}

func (rt *runtimeState) handleSuggestDashboardHTMLCommand(args string) error {
	if rt == nil || rt.session == nil {
		return fmt.Errorf("no active session")
	}
	mem := rt.session.ensureSuggestionMemory()
	snapshot := BuildSituationSnapshot(rt.proactiveSources())
	reportsDir := filepath.Join(userConfigDir(), "reports")
	if err := os.MkdirAll(reportsDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(reportsDir, "suggestions-"+time.Now().Format("20060102-150405")+".html")
	if err := os.WriteFile(path, []byte(renderSuggestionDashboardHTML(snapshot, mem)), 0o644); err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("Suggestion dashboard written: "+path))
	if rt.interactive {
		if err := OpenExternalURL(path); err != nil {
			fmt.Fprintln(rt.writer, rt.ui.hintLine("Open manually: "+path))
		}
	}
	return nil
}

func renderSuggestionDashboardHTML(snapshot SituationSnapshot, mem *SuggestionMemory) string {
	mode := SuggestionModeSuggest
	if mem != nil {
		mode = mem.modeOrDefault()
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Kernforge Suggestions</title>
<style>
body { margin: 0; font-family: Segoe UI, Arial, sans-serif; background: #18181b; color: #e5e7eb; }
main { max-width: 1120px; margin: 0 auto; padding: 32px 20px; }
h1 { margin: 0 0 6px; font-size: 28px; }
.subtle, .meta { color: #9ca3af; font-size: 13px; }
.grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); gap: 14px; margin-top: 18px; }
.card { border: 1px solid #3f3f46; border-radius: 8px; padding: 16px; background: #27272a; }
.card h2 { margin: 8px 0; font-size: 18px; }
section { margin-top: 22px; }
.section-title { margin: 0 0 10px; font-size: 15px; color: #d4d4d8; text-transform: uppercase; letter-spacing: 0; }
code { display: inline-block; margin-top: 8px; padding: 6px 8px; border-radius: 6px; background: #111113; color: #bfdbfe; }
pre { white-space: pre-wrap; background: #111113; border: 1px solid #3f3f46; border-radius: 8px; padding: 14px; }
.chips { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 10px; }
.chip { border: 1px solid #52525b; border-radius: 999px; color: #dbeafe; padding: 4px 9px; font-size: 12px; background: #18181b; }
.badge { border-radius: 999px; padding: 3px 8px; font-size: 12px; background: #3f3f46; color: #e4e4e7; }
.row { display: flex; justify-content: space-between; gap: 12px; align-items: baseline; }
.empty { color: #a1a1aa; border: 1px dashed #52525b; border-radius: 8px; padding: 16px; }
</style>
</head>
<body>
<main>
<h1>Proactive Situation Judgment</h1>
<div class="subtle">mode=%s generated=%s</div>
<pre>%s</pre>
<section>
<h2 class="section-title">Integrated Signals</h2>
<div class="grid">%s</div>
</section>
<section>
<h2 class="section-title">Suggested Next Actions</h2>
<div class="grid">%s</div>
</section>
</main>
</body>
</html>
`,
		htmlEscape(mode),
		htmlEscape(snapshot.CreatedAt.Format(time.RFC3339)),
		htmlEscape(renderSituationSnapshot(snapshot)),
		renderSuggestionSignalCardsHTML(snapshot),
		renderSuggestionCardsHTML(snapshot, mem),
	)
}

func renderSuggestionSignalCardsHTML(snapshot SituationSnapshot) string {
	cards := []string{
		suggestionSignalCardHTML("Verification", len(snapshot.MissingVerification), "/verify-dashboard-html", snapshot.MissingVerification),
		suggestionSignalCardHTML("Analysis docs", len(snapshot.StaleDocs), "/analyze-dashboard", snapshot.StaleDocs),
		suggestionSignalCardHTML("Evidence", len(snapshot.MissingEvidence), "/evidence-dashboard-html", snapshot.MissingEvidence),
		suggestionSignalCardHTML("Changed paths", len(snapshot.ChangedPaths), "/checkpoint-diff", snapshot.ChangedPaths),
	}
	return strings.Join(cards, "\n")
}

func suggestionSignalCardHTML(title string, count int, command string, items []string) string {
	body := "No current gap detected."
	if count > 0 {
		body = strings.Join(limitStrings(items, 4), "; ")
	}
	return fmt.Sprintf(`<article class="card"><div class="row"><h2>%s</h2><span class="badge">%d</span></div><p class="subtle">%s</p><code>%s</code></article>`,
		htmlEscape(title),
		count,
		htmlEscape(body),
		htmlEscape(command),
	)
}

func renderSuggestionCardsHTML(snapshot SituationSnapshot, mem *SuggestionMemory) string {
	suggestionCards := []string{}
	for _, item := range rankSuggestions(snapshot.SuggestionCandidates) {
		command := htmlEscape(valueOrDefault(item.Command, "manual follow-up"))
		links := renderSuggestionDashboardLinksHTML(item)
		evidenceRefs := renderSuggestionEvidenceRefsHTML(item)
		status := suggestionMemoryStatus(item, mem)
		suggestionCards = append(suggestionCards, fmt.Sprintf(
			`<article class="card"><div class="meta">%s | status=%s | risk=%s | cost=%s</div><h2>%s</h2><p>%s</p><code>%s</code>%s%s<div class="chips"><span class="chip">/suggest accept %s</span><span class="chip">/suggest dismiss %s</span></div></article>`,
			htmlEscape(item.ID),
			htmlEscape(status),
			htmlEscape(valueOrDefault(item.Risk, "unknown")),
			htmlEscape(valueOrDefault(item.EstimatedCost, "unknown")),
			htmlEscape(item.Title),
			htmlEscape(item.Reason),
			command,
			links,
			evidenceRefs,
			htmlEscape(item.ID),
			htmlEscape(item.ID),
		))
	}
	if len(suggestionCards) == 0 {
		return `<article class="empty"><h2>No suggestions</h2><p>No proactive suggestions for the current situation.</p></article>`
	}
	return strings.Join(suggestionCards, "\n")
}

func renderSuggestionDashboardLinksHTML(item Suggestion) string {
	links := suggestionDashboardLinks(item)
	if len(links) == 0 {
		return ""
	}
	chips := []string{}
	for _, link := range links {
		chips = append(chips, `<span class="chip">`+htmlEscape(link)+`</span>`)
	}
	return `<div class="chips"><span class="meta">Dashboard links</span>` + strings.Join(chips, "") + `</div>`
}

func renderSuggestionEvidenceRefsHTML(item Suggestion) string {
	refs := limitStrings(item.EvidenceRefs, 6)
	if len(refs) == 0 {
		return ""
	}
	chips := []string{}
	for _, ref := range refs {
		chips = append(chips, `<span class="chip">`+htmlEscape(ref)+`</span>`)
	}
	return `<div class="chips"><span class="meta">Evidence refs</span>` + strings.Join(chips, "") + `</div>`
}

func suggestionDashboardLinks(item Suggestion) []string {
	switch strings.TrimSpace(item.Type) {
	case "run_verification", "inspect_failure":
		return []string{"/verify-dashboard-html", "/evidence-dashboard-html"}
	case "refresh_analysis":
		return []string{"/analyze-dashboard", "/docs-refresh"}
	case "evidence_capture":
		return []string{"/evidence-dashboard-html", "/evidence"}
	case "fuzz_next_step":
		return []string{"/fuzz-campaign status", "/evidence-dashboard-html"}
	case "checkpoint_or_worktree":
		return []string{"/checkpoints", "/checkpoint-diff"}
	case "retry_or_switch_model":
		return []string{"/provider status", "/model"}
	case "continue_workflow", "cleanup_or_close_feature":
		return []string{"/tasks", "/suggest"}
	default:
		if strings.TrimSpace(item.Command) != "" {
			return []string{item.Command}
		}
	}
	return nil
}

func normalizeSuggestion(item Suggestion) Suggestion {
	item.ID = strings.TrimSpace(item.ID)
	item.Type = strings.TrimSpace(item.Type)
	item.Title = strings.TrimSpace(item.Title)
	item.Reason = compactPromptSection(item.Reason, 260)
	item.Command = strings.TrimSpace(item.Command)
	item.EstimatedCost = strings.TrimSpace(item.EstimatedCost)
	item.Risk = strings.TrimSpace(item.Risk)
	item.DedupKey = strings.TrimSpace(item.DedupKey)
	if item.DedupKey == "" {
		item.DedupKey = strings.Join([]string{item.Type, item.Title, item.Command}, ":")
	}
	if item.ID == "" {
		item.ID = "sg-" + shortStableID(item.DedupKey)
	}
	item.EvidenceRefs = uniqueStrings(item.EvidenceRefs)
	return item
}

func uniqueSuggestions(items []Suggestion) []Suggestion {
	out := []Suggestion{}
	seen := map[string]bool{}
	for _, item := range items {
		item = normalizeSuggestion(item)
		key := strings.ToLower(item.DedupKey)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}

func rankSuggestions(items []Suggestion) []Suggestion {
	out := append([]Suggestion(nil), items...)
	sort.SliceStable(out, func(i, j int) bool {
		left := suggestionPriority(out[i])
		right := suggestionPriority(out[j])
		if left != right {
			return left > right
		}
		return out[i].Title < out[j].Title
	})
	return out
}

func suggestionPriority(item Suggestion) int {
	score := 0
	switch item.Type {
	case "retry_or_switch_model", "inspect_failure":
		score += 100
	case "checkpoint_or_worktree":
		score += 90
	case "run_verification":
		score += 80
	case "fuzz_next_step":
		score += 70
	case "refresh_analysis":
		score += 60
	case "evidence_capture", "continue_workflow":
		score += 50
	}
	switch strings.ToLower(item.Risk) {
	case "critical":
		score += 30
	case "high":
		score += 20
	case "medium":
		score += 10
	}
	return score
}

func latestProviderRateLimitEvent(sess *Session) (ConversationEvent, bool) {
	if sess == nil {
		return ConversationEvent{}, false
	}
	for i := len(sess.ConversationEvents) - 1; i >= 0; i-- {
		event := sess.ConversationEvents[i]
		if event.Kind != conversationEventKindProviderError {
			continue
		}
		category := strings.ToLower(strings.TrimSpace(event.Entities["category"]))
		code := strings.TrimSpace(event.Entities["code"])
		if category == "rate_limit" || category == "timeout" || code == "429" || strings.Contains(strings.ToLower(event.Summary), "too many requests") {
			return event, true
		}
	}
	return ConversationEvent{}, false
}

func latestFailedVerification(sess *Session) string {
	if sess == nil || sess.LastVerification == nil || !sess.LastVerification.HasFailures() {
		return ""
	}
	return sess.LastVerification.FailureSummary()
}

func pendingWorkflowSuggestion(sess *Session) string {
	if sess == nil || sess.ConversationState == nil {
		return ""
	}
	return strings.TrimSpace(sess.ConversationState.PendingNextStep)
}

func cleanupFeatureSuggestion(sess *Session) string {
	if sess == nil || strings.TrimSpace(sess.ActiveFeatureID) == "" || sess.LastVerification == nil {
		return ""
	}
	if sess.LastVerification.HasFailures() {
		return ""
	}
	return "active feature " + sess.ActiveFeatureID + " has a passing latest verification; consider closing or summarizing it"
}

func evidenceCaptureGap(src ProactiveSources) string {
	if src.Session == nil || src.Session.LastVerification == nil || src.Evidence == nil {
		return ""
	}
	if !src.Session.LastVerification.HasFailures() {
		return ""
	}
	records, err := src.Evidence.ListRecent(workspaceSnapshotRoot(src.Workspace), 8)
	if err != nil {
		return ""
	}
	for _, record := range records {
		if strings.EqualFold(record.Kind, "verification_failure") {
			return ""
		}
	}
	return "latest verification failed but no recent verification_failure evidence record was found"
}

func fuzzSuggestionGaps(src ProactiveSources) []string {
	out := []string{}
	root := workspaceSnapshotRoot(src.Workspace)
	if src.FunctionFuzz != nil && src.FuzzCampaigns != nil {
		runs, err := src.FunctionFuzz.ListRecent(root, 1)
		if err == nil && len(runs) > 0 {
			run := runs[0]
			if len(run.VirtualScenarios) > 0 {
				campaigns, _ := src.FuzzCampaigns.ListRecent(root, 1)
				if len(campaigns) == 0 || !containsString(campaigns[0].FunctionRuns, run.ID) || len(campaigns[0].NativeResults) == 0 {
					out = append(out, "source-only fuzz scenarios exist for "+firstNonBlankString(run.TargetSymbolName, run.ID)+" but no native campaign result is attached")
				}
			}
		}
	}
	if src.FuzzCampaigns != nil {
		campaigns, err := src.FuzzCampaigns.ListRecent(root, 1)
		if err == nil && len(campaigns) > 0 {
			campaign := campaigns[0]
			for _, result := range campaign.NativeResults {
				if (result.CrashCount > 0 || len(result.ArtifactIDs) > 0) && strings.TrimSpace(result.MinimizeCommand) == "" {
					out = append(out, "native fuzz crash exists in campaign "+campaign.ID+" for "+firstNonBlankString(result.Target, result.RunID)+" but no minimization command is recorded")
					break
				}
			}
			if len(campaign.CoverageGaps) > 0 {
				out = append(out, "campaign "+campaign.ID+" has coverage gaps: "+campaign.CoverageGaps[0].Reason)
			}
		}
	}
	return uniqueStrings(out)
}

func gitChangedPaths(root string) []string {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "status", "--short")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	paths := []string{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = strings.TrimSpace(path[idx+4:])
		}
		if path != "" {
			paths = append(paths, filepath.ToSlash(path))
		}
	}
	return uniqueStrings(paths)
}

func latestAnalysisStaleDocs(root string) []string {
	manifest, ok := loadLatestAnalysisDocsManifest(root)
	if !ok {
		return nil
	}
	out := []string{}
	for _, doc := range manifest.Documents {
		if len(doc.StaleMarkers) > 0 {
			out = append(out, doc.Name+": "+strings.Join(limitStrings(doc.StaleMarkers, 2), ", "))
		}
		for _, section := range doc.Sections {
			if len(section.StaleMarkers) > 0 {
				out = append(out, doc.Name+"#"+section.ID+": "+strings.Join(limitStrings(section.StaleMarkers, 2), ", "))
			}
		}
	}
	return uniqueStrings(out)
}

func recentVerificationCoversChanges(sess *Session, history *VerificationHistoryStore, root string, changed []string) bool {
	if len(changed) == 0 {
		return true
	}
	if sess != nil && sess.LastVerification != nil && len(sess.LastVerification.ChangedPaths) > 0 {
		return changedPathsCovered(changed, sess.LastVerification.ChangedPaths)
	}
	if history == nil {
		return false
	}
	dashboard, err := history.Dashboard(root, false, nil, 1)
	if err != nil || len(dashboard.Recent) == 0 {
		return false
	}
	entry := dashboard.Recent[0]
	if time.Since(entry.RecordedAt) > 2*time.Hour {
		return false
	}
	return changedPathsCovered(changed, entry.Report.ChangedPaths)
}

func changedPathsCovered(changed []string, verified []string) bool {
	if len(verified) == 0 {
		return false
	}
	verifiedSet := map[string]bool{}
	for _, path := range verified {
		verifiedSet[strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))] = true
	}
	for _, path := range changed {
		key := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
		if !verifiedSet[key] {
			return false
		}
	}
	return true
}

func verificationGapForChangedPaths(paths []string) string {
	risk := riskLevelForChangedPaths(paths)
	switch risk {
	case "high":
		return "high-risk Windows security/kernel/anti-cheat files changed without a covering verification report"
	case "medium":
		return "security-sensitive source files changed without a covering verification report"
	default:
		return "workspace has changed files without a covering verification report"
	}
}

func riskLevelForChangedPaths(paths []string) string {
	joined := strings.ToLower(strings.Join(paths, " "))
	switch {
	case containsAny(joined, "driver", "kernel", ".sys", "ioctl", "tpm", "process_protection", "anti", "cheat", "memory", "scan", "etw", "telemetry"):
		return "high"
	case containsAny(joined, ".cpp", ".cc", ".c", ".h", ".hpp", ".cs", ".go"):
		return "medium"
	default:
		return "low"
	}
}

func shouldSuggestCheckpoint(snapshot SituationSnapshot) bool {
	if len(snapshot.ChangedPaths) == 0 {
		return false
	}
	return snapshot.RiskLevel == "high"
}

func extractCommandCandidate(text string) string {
	for _, field := range strings.Fields(text) {
		field = strings.Trim(field, "`'\".,;()[]")
		if strings.HasPrefix(field, "/") {
			return field
		}
	}
	return ""
}

func shortStableID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return time.Now().Format("150405000")
	}
	hash := uint32(2166136261)
	for _, b := range []byte(value) {
		hash ^= uint32(b)
		hash *= 16777619
	}
	return fmt.Sprintf("%08x", hash)
}
