package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	AutomationTypeRecurringVerification = "recurring_verification"
	AutomationTypePRReview              = "pr_review"

	AutomationStatusActive   = "active"
	AutomationStatusPaused   = "paused"
	AutomationStatusRunning  = "running"
	AutomationStatusComplete = "complete"
	AutomationStatusFailed   = "failed"
)

type SessionAutomation struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	Name        string    `json:"name"`
	Command     string    `json:"command"`
	Status      string    `json:"status"`
	Schedule    string    `json:"schedule,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
	LastRunAt   time.Time `json:"last_run_at,omitempty"`
	NextRunHint string    `json:"next_run_hint,omitempty"`
	LastResult  string    `json:"last_result,omitempty"`
}

func (s *Session) normalizeAutomations() {
	if s == nil {
		return
	}
	items := make([]SessionAutomation, 0, len(s.Automations))
	seen := map[string]struct{}{}
	for _, item := range s.Automations {
		item = normalizeSessionAutomation(item)
		if item.ID == "" {
			continue
		}
		key := strings.ToLower(item.ID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, item)
	}
	if len(items) > 40 {
		items = append([]SessionAutomation(nil), items[len(items)-40:]...)
	}
	s.Automations = items
}

func normalizeSessionAutomation(item SessionAutomation) SessionAutomation {
	now := time.Now()
	item.ID = strings.TrimSpace(item.ID)
	item.Type = strings.TrimSpace(strings.ToLower(item.Type))
	item.Name = strings.TrimSpace(item.Name)
	item.Command = strings.TrimSpace(item.Command)
	item.Status = strings.TrimSpace(strings.ToLower(item.Status))
	item.Schedule = strings.TrimSpace(item.Schedule)
	item.NextRunHint = strings.TrimSpace(item.NextRunHint)
	item.LastResult = compactPromptSection(item.LastResult, 360)
	if item.Type == "" {
		item.Type = AutomationTypeRecurringVerification
	}
	if item.Name == "" {
		item.Name = strings.ReplaceAll(item.Type, "_", " ")
	}
	if item.Command == "" {
		if item.Type == AutomationTypePRReview {
			item.Command = "/review-pr"
		} else {
			item.Command = "/verify"
		}
	}
	switch item.Status {
	case "", AutomationStatusActive, AutomationStatusPaused, AutomationStatusRunning, AutomationStatusComplete, AutomationStatusFailed:
		if item.Status == "" {
			item.Status = AutomationStatusActive
		}
	default:
		item.Status = AutomationStatusActive
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = item.CreatedAt
	}
	return item
}

func (rt *runtimeState) handleAutomationCommand(args string) error {
	if rt == nil || rt.session == nil {
		return fmt.Errorf("no active session")
	}
	rt.session.normalizeAutomations()
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) == 0 || strings.EqualFold(fields[0], "list") || strings.EqualFold(fields[0], "status") {
		rt.printAutomationStatus()
		return rt.store.Save(rt.session)
	}
	switch strings.ToLower(fields[0]) {
	case "add":
		return rt.handleAutomationAdd(fields[1:])
	case "run":
		if len(fields) < 2 {
			return fmt.Errorf("usage: /automation run <id>")
		}
		return rt.runAutomation(fields[1])
	case "pause", "resume", "remove":
		if len(fields) < 2 {
			return fmt.Errorf("usage: /automation %s <id>", strings.ToLower(fields[0]))
		}
		return rt.updateAutomationState(strings.ToLower(fields[0]), fields[1])
	default:
		return fmt.Errorf("usage: /automation [list|add recurring-verification|add pr-review|run <id>|pause <id>|resume <id>|remove <id>]")
	}
}

func (rt *runtimeState) handleAutomationAdd(fields []string) error {
	if len(fields) == 0 {
		return fmt.Errorf("usage: /automation add <recurring-verification|pr-review> [command]")
	}
	typ := strings.ToLower(strings.TrimSpace(fields[0]))
	command := strings.TrimSpace(strings.Join(fields[1:], " "))
	switch typ {
	case "verification", "recurring-verification", AutomationTypeRecurringVerification:
		typ = AutomationTypeRecurringVerification
		if command == "" {
			command = "/verify"
		}
	case "pr", "pr-review", AutomationTypePRReview:
		typ = AutomationTypePRReview
		if command == "" {
			command = "/review-pr"
		}
	default:
		return fmt.Errorf("unknown automation type: %s", fields[0])
	}
	if err := validateAutomationCommand(typ, command); err != nil {
		return err
	}
	item := normalizeSessionAutomation(SessionAutomation{
		ID:          automationID(typ, command),
		Type:        typ,
		Command:     command,
		Status:      AutomationStatusActive,
		Schedule:    "manual-recurring",
		NextRunHint: "run with /automation run " + automationID(typ, command),
	})
	for index := range rt.session.Automations {
		if strings.EqualFold(rt.session.Automations[index].ID, item.ID) {
			rt.session.Automations[index] = item
			fmt.Fprintln(rt.writer, rt.ui.successLine("Updated automation: "+item.ID))
			return rt.store.Save(rt.session)
		}
	}
	rt.session.Automations = append(rt.session.Automations, item)
	fmt.Fprintln(rt.writer, rt.ui.successLine("Added automation: "+item.ID))
	return rt.store.Save(rt.session)
}

func automationID(typ string, command string) string {
	key := strings.TrimSpace(typ) + ":" + strings.TrimSpace(command)
	return "auto-" + shortStableID(key)
}

func validateAutomationCommand(typ string, command string) error {
	cmd, ok := ParseCommand(command)
	if !ok {
		return fmt.Errorf("automation command must be a slash command: %s", command)
	}
	switch strings.TrimSpace(typ) {
	case AutomationTypeRecurringVerification:
		switch cmd.Name {
		case "verify", "verify-dashboard", "verify-dashboard-html":
			return nil
		default:
			return fmt.Errorf("recurring verification automation only allows /verify and verification dashboard commands")
		}
	case AutomationTypePRReview:
		if cmd.Name == "review-pr" {
			return nil
		}
		return fmt.Errorf("PR review automation only allows /review-pr")
	default:
		return fmt.Errorf("unknown automation type: %s", typ)
	}
}

func (rt *runtimeState) printAutomationStatus() {
	fmt.Fprintln(rt.writer, rt.ui.section("Automations"))
	if len(rt.session.Automations) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No automations configured."))
		return
	}
	for _, item := range rt.session.Automations {
		line := fmt.Sprintf("%s  type=%s  status=%s  command=%s", item.ID, item.Type, item.Status, item.Command)
		if strings.TrimSpace(item.LastResult) != "" {
			line += "  result=" + compactPromptSection(item.LastResult, 120)
		}
		fmt.Fprintln(rt.writer, line)
	}
}

func (rt *runtimeState) updateAutomationState(action string, id string) error {
	for index := range rt.session.Automations {
		if !strings.EqualFold(rt.session.Automations[index].ID, strings.TrimSpace(id)) {
			continue
		}
		switch action {
		case "pause":
			rt.session.Automations[index].Status = AutomationStatusPaused
		case "resume":
			rt.session.Automations[index].Status = AutomationStatusActive
		case "remove":
			rt.session.Automations = append(rt.session.Automations[:index], rt.session.Automations[index+1:]...)
			fmt.Fprintln(rt.writer, rt.ui.successLine("Removed automation: "+id))
			return rt.store.Save(rt.session)
		}
		rt.session.Automations[index].UpdatedAt = time.Now()
		fmt.Fprintln(rt.writer, rt.ui.successLine("Updated automation: "+id))
		return rt.store.Save(rt.session)
	}
	return fmt.Errorf("automation not found: %s", id)
}

func (rt *runtimeState) runAutomation(id string) error {
	for index := range rt.session.Automations {
		if !strings.EqualFold(rt.session.Automations[index].ID, strings.TrimSpace(id)) {
			continue
		}
		item := rt.session.Automations[index]
		if item.Status == AutomationStatusPaused {
			return fmt.Errorf("automation is paused: %s", id)
		}
		rt.session.Automations[index].Status = AutomationStatusRunning
		rt.session.Automations[index].LastRunAt = time.Now()
		rt.session.Automations[index].UpdatedAt = time.Now()
		result, err := rt.executeSafeSuggestionCommand(item.Command)
		if err != nil {
			rt.session.Automations[index].Status = AutomationStatusFailed
			rt.session.Automations[index].LastResult = err.Error()
			_ = rt.store.Save(rt.session)
			return err
		}
		rt.session.Automations[index].Status = AutomationStatusActive
		rt.session.Automations[index].LastResult = result
		rt.session.AppendConversationEvent(ConversationEvent{
			Kind:     conversationEventKindVerification,
			Severity: conversationSeverityInfo,
			Summary:  "automation run completed: " + item.ID,
			Entities: map[string]string{
				"automation_id": item.ID,
				"automation":    item.Type,
				"command":       item.Command,
			},
		})
		fmt.Fprintln(rt.writer, rt.ui.successLine("Automation completed: "+item.ID))
		return rt.store.Save(rt.session)
	}
	return fmt.Errorf("automation not found: %s", id)
}

func (rt *runtimeState) executeSafeSuggestionCommand(command string) (string, error) {
	cmd, ok := ParseCommand(command)
	if !ok {
		return "", fmt.Errorf("suggestion command must be a slash command: %s", command)
	}
	switch cmd.Name {
	case "verify":
		if err := rt.handleVerifyCommand(cmd.Args); err != nil {
			return "", err
		}
		return "executed /verify", nil
	case "verify-dashboard":
		if err := rt.handleVerifyDashboardCommand(cmd.Args); err != nil {
			return "", err
		}
		return "executed /verify-dashboard", nil
	case "verify-dashboard-html":
		if err := rt.handleVerifyDashboardHTMLCommand(cmd.Args); err != nil {
			return "", err
		}
		return "executed /verify-dashboard-html", nil
	case "suggest-dashboard-html":
		if err := rt.handleSuggestDashboardHTMLCommand(cmd.Args); err != nil {
			return "", err
		}
		return "executed /suggest-dashboard-html", nil
	case "evidence-dashboard":
		if err := rt.handleEvidenceDashboard(cmd.Args, false); err != nil {
			return "", err
		}
		return "executed /evidence-dashboard", nil
	case "evidence-dashboard-html":
		if err := rt.handleEvidenceDashboard(cmd.Args, true); err != nil {
			return "", err
		}
		return "executed /evidence-dashboard-html", nil
	case "docs-refresh":
		if err := rt.handleDocsRefreshCommand(cmd.Args); err != nil {
			return "", err
		}
		return "executed /docs-refresh", nil
	case "analyze-dashboard":
		if err := rt.handleAnalyzeDashboardCommand(cmd.Args); err != nil {
			return "", err
		}
		return "executed /analyze-dashboard", nil
	case "review-pr":
		if err := rt.handlePRReviewAutomationCommand(cmd.Args); err != nil {
			return "", err
		}
		return "executed /review-pr", nil
	case "automation":
		automationFields := strings.Fields(strings.TrimSpace(cmd.Args))
		if len(automationFields) == 0 || !strings.EqualFold(automationFields[0], "add") {
			return "", fmt.Errorf("only /automation add is allowed from suggestion execution")
		}
		if err := rt.handleAutomationCommand(cmd.Args); err != nil {
			return "", err
		}
		return "executed /automation add", nil
	default:
		return "", fmt.Errorf("command is not allowed for automatic execution: /%s", cmd.Name)
	}
}

func (rt *runtimeState) handlePRReviewAutomationCommand(args string) error {
	root := workspaceSnapshotRoot(rt.workspace)
	if strings.TrimSpace(root) == "" {
		root = rt.workspace.Root
	}
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("workspace root is not configured")
	}
	branch := runGitText(root, "rev-parse", "--abbrev-ref", "HEAD")
	status := runGitText(root, "status", "--short")
	diffStat := runGitText(root, "diff", "--stat")
	nameOnly := runGitText(root, "diff", "--name-only")
	outDir := filepath.Join(root, ".kernforge", "pr_review")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(outDir, "latest.md")
	body := renderPRReviewAutomationReport(branch, status, diffStat, nameOnly)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err
	}
	rt.session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindHandoff,
		Severity: conversationSeverityInfo,
		Summary:  "PR review automation report generated",
		ArtifactRefs: []string{
			path,
		},
		Entities: map[string]string{
			"automation": "pr_review",
			"branch":     strings.TrimSpace(branch),
		},
	})
	fmt.Fprintln(rt.writer, rt.ui.successLine("Generated PR review automation report: "+path))
	return nil
}

func runGitText(root string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	data, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(data) + "\n" + err.Error())
	}
	return strings.TrimSpace(string(data))
}

func renderPRReviewAutomationReport(branch string, status string, diffStat string, nameOnly string) string {
	now := time.Now().Format(time.RFC3339)
	if strings.TrimSpace(status) == "" {
		status = "clean"
	}
	if strings.TrimSpace(diffStat) == "" {
		diffStat = "no unstaged diff"
	}
	if strings.TrimSpace(nameOnly) == "" {
		nameOnly = "no changed files"
	}
	return strings.TrimSpace(fmt.Sprintf(`# PR Review Automation

Generated: %s
Branch: %s

## Status

%s

## Diff Stat

%s

## Changed Files

%s

## Review Checklist

- Correctness: inspect modified control flow and error paths.
- Security: check trust boundaries, input validation, and risky Windows/security surfaces.
- Stability: confirm verification coverage is current.
- Maintainability: ensure docs and handoff artifacts match the implementation.
`, now, valueOrDefault(strings.TrimSpace(branch), "unknown"), status, diffStat, nameOnly))
}

func (rt *runtimeState) promoteSuggestionPreferenceToMemory(record SuggestionRecord, status string) error {
	if rt == nil || rt.longMem == nil || rt.session == nil {
		return nil
	}
	suggestion := record.Suggestion
	status = strings.TrimSpace(status)
	if status == "" {
		status = record.Status
	}
	summary := fmt.Sprintf("Suggestion %s: %s", status, suggestion.Title)
	if strings.TrimSpace(suggestion.Command) != "" {
		summary += " command=" + suggestion.Command
	}
	id := "suggestion-" + status + "-" + shortStableID(suggestion.DedupKey+"-"+status)
	if _, ok, err := rt.longMem.Get(id); err != nil {
		return err
	} else if ok {
		return nil
	}
	return rt.longMem.Append(PersistentMemoryRecord{
		ID:                     id,
		SessionID:              rt.session.ID,
		SessionName:            rt.session.Name,
		Provider:               rt.session.Provider,
		Model:                  rt.session.Model,
		Workspace:              workspaceSnapshotRoot(rt.workspace),
		CreatedAt:              time.Now(),
		Request:                "/suggest " + status + " " + suggestion.ID,
		Reply:                  summary,
		Summary:                summary,
		Importance:             PersistentMemoryMedium,
		Trust:                  PersistentMemoryConfirmed,
		VerificationCategories: []string{"suggestion-preference"},
		VerificationTags:       []string{status, suggestion.Type},
		Files:                  append([]string(nil), suggestion.EvidenceRefs...),
		Keywords:               []string{"suggestion", status, suggestion.Type, suggestion.Command},
	})
}

func (rt *runtimeState) syncSuggestionToTaskGraph(record SuggestionRecord) {
	if rt == nil || rt.session == nil {
		return
	}
	suggestion := record.Suggestion
	if strings.TrimSpace(suggestion.DedupKey) == "" {
		return
	}
	status := "ready"
	switch strings.TrimSpace(record.Status) {
	case SuggestionStatusAccepted:
		status = "in_progress"
	case SuggestionStatusDismissed:
		status = "canceled"
	case SuggestionStatusExecuted:
		status = "completed"
	}
	graph := rt.session.EnsureTaskGraph()
	if graph == nil {
		return
	}
	node := TaskNode{
		ID:                 "suggest:" + shortStableID(suggestion.DedupKey),
		Title:              "Suggestion: " + suggestion.Title,
		Kind:               suggestionTaskKind(suggestion),
		Status:             status,
		ReadOnlyWorkerTool: suggestion.Command,
		LifecycleNote:      compactPromptSection(suggestion.Reason, 220),
		LastUpdated:        time.Now(),
	}
	graph.UpsertNode(node)
}

func (rt *runtimeState) syncSuggestionCandidatesToTaskGraph(items []Suggestion, mem *SuggestionMemory) {
	for _, item := range items {
		rt.syncSuggestionToTaskGraph(SuggestionRecord{
			Suggestion: item,
			Status:     suggestionMemoryStatus(item, mem),
		})
	}
}

func suggestionTaskKind(item Suggestion) string {
	switch strings.TrimSpace(strings.ToLower(item.Type)) {
	case "run_verification", "inspect_failure", AutomationTypeRecurringVerification:
		return "verification"
	case "refresh_analysis", "retry_or_switch_model":
		return "inspection"
	case "evidence_capture", "fuzz_next_step", "checkpoint_or_worktree", "continue_workflow", "cleanup_or_close_feature":
		return "task"
	default:
		return inferTaskNodeKind(item.Title + " " + item.Command)
	}
}

func sessionHasActiveAutomation(sess *Session, typ string) bool {
	if sess == nil {
		return false
	}
	sess.normalizeAutomations()
	for _, item := range sess.Automations {
		if strings.EqualFold(item.Type, strings.TrimSpace(typ)) && item.Status == AutomationStatusActive {
			return true
		}
	}
	return false
}
