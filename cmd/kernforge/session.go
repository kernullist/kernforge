package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type PlanItem struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

type ReviewRepairConfirmationState struct {
	CreatedAt time.Time `json:"created_at"`
	ReviewID  string    `json:"review_id,omitempty"`
	Verdict   string    `json:"verdict,omitempty"`
	Mode      string    `json:"mode,omitempty"`
}

type Session struct {
	ID                              string                         `json:"id"`
	Name                            string                         `json:"name"`
	BaseWorkingDir                  string                         `json:"base_working_dir,omitempty"`
	WorkingDir                      string                         `json:"working_dir"`
	Worktree                        *SessionWorktree               `json:"worktree,omitempty"`
	SpecialistWorktrees             []SpecialistWorktree           `json:"specialist_worktrees,omitempty"`
	Provider                        string                         `json:"provider"`
	Model                           string                         `json:"model"`
	BaseURL                         string                         `json:"base_url,omitempty"`
	PermissionMode                  string                         `json:"permission_mode"`
	CreatedAt                       time.Time                      `json:"created_at"`
	UpdatedAt                       time.Time                      `json:"updated_at"`
	Summary                         string                         `json:"summary,omitempty"`
	LastAnalysisContextQuery        string                         `json:"last_analysis_context_query,omitempty"`
	LastAnalysisContextRunID        string                         `json:"last_analysis_context_run_id,omitempty"`
	ActiveFeatureID                 string                         `json:"active_feature_id,omitempty"`
	LastFeatureID                   string                         `json:"last_feature_id,omitempty"`
	Plan                            []PlanItem                     `json:"plan,omitempty"`
	LastAnalysis                    *ProjectAnalysisSummary        `json:"last_analysis,omitempty"`
	LastVerification                *VerificationReport            `json:"last_verification,omitempty"`
	AcceptanceContract              *AcceptanceContract            `json:"acceptance_contract,omitempty"`
	ActivePatchTransaction          *PatchTransaction              `json:"active_patch_transaction,omitempty"`
	PatchTransactions               []PatchTransaction             `json:"patch_transactions,omitempty"`
	ActiveEditLoop                  *EditLoopState                 `json:"active_edit_loop,omitempty"`
	EditLoops                       []EditLoopState                `json:"edit_loops,omitempty"`
	LastCodingHarnessReport         *CodingHarnessReport           `json:"last_coding_harness_report,omitempty"`
	LastDocumentArtifactFingerprint string                         `json:"last_document_artifact_fingerprint,omitempty"`
	LastUserChangeIsolationReport   *UserChangeIsolationReport     `json:"last_user_change_isolation_report,omitempty"`
	LastTestImpactReport            *TestImpactReport              `json:"last_test_impact_report,omitempty"`
	LastJobSupervisorReport         *JobSupervisorReport           `json:"last_job_supervisor_report,omitempty"`
	LastReviewRun                   *ReviewRun                     `json:"last_review_run,omitempty"`
	PendingReviewRepairConfirm      *ReviewRepairConfirmationState `json:"pending_review_repair_confirmation,omitempty"`
	ReviewRouteHealth               []ReviewRouteHealth            `json:"review_route_health,omitempty"`
	SecondPassReviewCache           []SecondPassReviewCacheEntry   `json:"second_pass_review_cache,omitempty"`
	ExternalLookupIntents           []ReviewExternalLookupIntent   `json:"external_lookup_intents,omitempty"`
	RuntimeGateLedger               *RuntimeGateLedger             `json:"runtime_gate_ledger,omitempty"`
	ActiveFailureRepair             *FailureRepairAttempt          `json:"active_failure_repair,omitempty"`
	FailureRepairAttempts           []FailureRepairAttempt         `json:"failure_repair_attempts,omitempty"`
	LastSelection                   *ViewerSelection               `json:"last_selection,omitempty"`
	Selections                      []ViewerSelection              `json:"selections,omitempty"`
	ActiveSelection                 int                            `json:"active_selection,omitempty"`
	TaskState                       *TaskState                     `json:"task_state,omitempty"`
	TaskGraph                       *TaskGraph                     `json:"task_graph,omitempty"`
	BackgroundJobs                  []BackgroundShellJob           `json:"background_jobs,omitempty"`
	BackgroundBundles               []BackgroundShellBundle        `json:"background_bundles,omitempty"`
	ConversationEvents              []ConversationEvent            `json:"conversation_events,omitempty"`
	ConversationState               *ConversationState             `json:"conversation_state,omitempty"`
	SuggestionMemory                *SuggestionMemory              `json:"suggestion_memory,omitempty"`
	Automations                     []SessionAutomation            `json:"automations,omitempty"`
	ActiveGoalID                    string                         `json:"active_goal_id,omitempty"`
	Goals                           []GoalState                    `json:"goals,omitempty"`
	Messages                        []Message                      `json:"messages"`
}

func NewSession(workingDir, providerName, model, baseURL, permissionMode string) *Session {
	now := time.Now()
	id := fmt.Sprintf("%s-%03d", now.Format("20060102-150405"), now.Nanosecond()/1_000_000)
	return &Session{
		ID:             id,
		Name:           "Session " + id,
		BaseWorkingDir: workingDir,
		WorkingDir:     workingDir,
		Provider:       providerName,
		Model:          model,
		BaseURL:        baseURL,
		PermissionMode: permissionMode,
		CreatedAt:      now,
		UpdatedAt:      now,
		Messages:       []Message{},
	}
}

func (s *Session) AddMessage(msg Message) {
	msg = normalizeSessionMessage(msg)
	s.Messages = append(s.Messages, msg)
	s.UpdatedAt = time.Now()
}

func normalizeSessionMessage(msg Message) Message {
	if strings.EqualFold(strings.TrimSpace(msg.Role), "user") && !msg.Internal && looksLikeInternalReviewFeedbackUserMessage(msg.Text) {
		msg.Internal = true
	}
	return msg
}

func (s *Session) ApproxChars() int {
	total := len(s.Summary)
	if s.TaskState != nil {
		total += s.TaskState.ApproxChars()
	}
	if s.TaskGraph != nil {
		total += len(s.TaskGraph.RenderExportSection())
	}
	if s.AcceptanceContract != nil {
		total += len(s.AcceptanceContract.RenderPromptSection())
	}
	if s.ActivePatchTransaction != nil {
		total += len(s.ActivePatchTransaction.RenderPromptSection())
	}
	for _, tx := range s.PatchTransactions {
		total += len(tx.RenderPromptSection())
	}
	if s.ActiveEditLoop != nil {
		total += len(s.ActiveEditLoop.RenderPromptSection())
	}
	for _, loop := range s.EditLoops {
		total += len(loop.RenderPromptSection())
	}
	if s.LastCodingHarnessReport != nil {
		total += len(s.LastCodingHarnessReport.RenderPromptSection())
	}
	total += len(s.LastDocumentArtifactFingerprint)
	if s.LastUserChangeIsolationReport != nil {
		total += len(s.LastUserChangeIsolationReport.RenderPromptSection())
	}
	if s.LastTestImpactReport != nil {
		total += len(s.LastTestImpactReport.RenderPromptSection())
	}
	if s.LastJobSupervisorReport != nil {
		total += len(s.LastJobSupervisorReport.RenderPromptSection())
	}
	if s.RuntimeGateLedger != nil {
		total += len(s.RuntimeGateLedger.RenderPromptSection())
	}
	if s.ActiveFailureRepair != nil {
		total += len(s.ActiveFailureRepair.RenderPromptSection())
	}
	for _, attempt := range s.FailureRepairAttempts {
		total += len(attempt.RenderPromptSection())
	}
	for _, lease := range s.SpecialistWorktrees {
		total += len(lease.Specialist) + len(lease.Root) + len(lease.Branch) + len(lease.LastOwnerNodeID)
		for _, item := range lease.OwnershipPaths {
			total += len(item)
		}
		for _, item := range lease.NodeIDs {
			total += len(item)
		}
	}
	for _, job := range s.BackgroundJobs {
		total += len(job.ID) + len(job.Command) + len(job.Status) + len(job.LastOutput)
	}
	for _, bundle := range s.BackgroundBundles {
		total += len(bundle.ID) + len(bundle.Status) + len(bundle.LastSummary)
		for _, item := range bundle.CommandSummaries {
			total += len(item)
		}
		for _, item := range bundle.JobIDs {
			total += len(item)
		}
	}
	if s.ConversationState != nil {
		total += s.ConversationState.ApproxChars()
	}
	if s.SuggestionMemory != nil {
		total += s.SuggestionMemory.ApproxChars()
	}
	for _, automation := range s.Automations {
		total += len(automation.ID) + len(automation.Type) + len(automation.Name)
		total += len(automation.Command) + len(automation.Status) + len(automation.Schedule)
		total += len(automation.NextRunHint) + len(automation.LastResult)
		if !automation.NextRunAt.IsZero() {
			total += len(automation.NextRunAt.Format(time.RFC3339))
		}
	}
	for _, goal := range s.Goals {
		total += len(goal.ID) + len(goal.Status) + len(goal.Objective) + len(goal.SourcePath)
		for _, iteration := range goal.Iterations {
			total += len(iteration.Status) + len(iteration.ReplySummary) + len(iteration.Error)
			total += len(iteration.Verification) + len(iteration.AuditStatus) + len(iteration.RecoveryStatus)
			for _, item := range iteration.Blockers {
				total += len(item)
			}
			for _, item := range iteration.Warnings {
				total += len(item)
			}
		}
	}
	for _, event := range s.ConversationEvents {
		total += len(event.Kind) + len(event.Severity) + len(event.Summary) + len(event.Raw) + len(event.TurnID) + len(event.TraceID) + len(event.CorrelationID)
		for key, value := range event.Entities {
			total += len(key) + len(value)
		}
		for _, ref := range event.ArtifactRefs {
			total += len(ref)
		}
	}
	for _, msg := range s.Messages {
		total += len(msg.Text) + len(msg.ReasoningContent) + encryptedReasoningApproxChars(len(msg.ReasoningEncryptedContent))
		for _, image := range msg.Images {
			total += messageImageApproxChars(s.WorkingDir, image)
		}
		for _, call := range msg.WebSearchCalls {
			total += len(call.ID) + len(call.Status)
			if encoded, err := json.Marshal(call.Action); err == nil {
				total += len(encoded)
			}
		}
		for _, call := range msg.LocalShellCalls {
			total += len(call.ID) + len(call.CallID) + len(call.Status)
			if encoded, err := json.Marshal(call.Action); err == nil {
				total += len(encoded)
			}
		}
		for _, item := range msg.CodexCompactionItems {
			total += encryptedReasoningApproxChars(len(item.EncryptedContent))
		}
		for _, item := range msg.CodexToolOutputItems {
			total += len(item.Type) + len(item.CallID) + len(item.Name) + len(item.Status) + len(item.Execution) + len(item.Text)
			for _, content := range item.ToolContentItems {
				total += toolContentItemApproxChars(content)
			}
			for _, tool := range item.Tools {
				if encoded, err := json.Marshal(tool); err == nil {
					total += len(encoded)
				}
			}
		}
		total += len(msg.ToolCallID) + len(msg.ToolName)
		for _, tc := range msg.ToolCalls {
			total += len(tc.ID) + len(tc.Name) + len(tc.Namespace) + len(tc.Status) + len(tc.Arguments)
		}
		for _, item := range msg.ToolContentItems {
			total += toolContentItemApproxChars(item)
		}
	}
	return total
}

func (s *Session) ExportText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", s.Name)
	fmt.Fprintf(&b, "Session ID: %s\n", s.ID)
	fmt.Fprintf(&b, "Working dir: %s\n", s.WorkingDir)
	if baseRoot := sessionBaseWorkingDir(s); baseRoot != "" && !strings.EqualFold(strings.TrimSpace(baseRoot), strings.TrimSpace(s.WorkingDir)) {
		fmt.Fprintf(&b, "Base working dir: %s\n", baseRoot)
	}
	if roots := workspaceEffectiveRoots(Workspace{}, s); len(roots) > 0 {
		fmt.Fprintf(&b, "Workspace roots: %s\n", strings.Join(roots, ", "))
	}
	fmt.Fprintf(&b, "Provider/model: %s / %s\n", s.Provider, s.Model)
	if strings.TrimSpace(s.BaseURL) != "" {
		fmt.Fprintf(&b, "Base URL: %s\n", s.BaseURL)
	}
	fmt.Fprintf(&b, "Permission mode: %s\n", s.PermissionMode)
	if profileID := activePermissionProfileIDForModeString(s.PermissionMode); profileID != "" {
		fmt.Fprintf(&b, "Active permission profile: %s\n", profileID)
	}
	b.WriteString("\n")
	if s.Worktree != nil && strings.TrimSpace(s.Worktree.Root) != "" {
		b.WriteString("## Worktree\n\n")
		fmt.Fprintf(&b, "- Root: %s\n", s.Worktree.Root)
		if strings.TrimSpace(s.Worktree.Branch) != "" {
			fmt.Fprintf(&b, "- Branch: %s\n", s.Worktree.Branch)
		}
		fmt.Fprintf(&b, "- Active: %t\n", s.Worktree.Active)
		fmt.Fprintf(&b, "- Managed: %t\n", s.Worktree.Managed)
		b.WriteString("\n")
	}
	if len(s.SpecialistWorktrees) > 0 {
		b.WriteString("## Specialist Worktrees\n\n")
		for _, lease := range s.SpecialistWorktrees {
			lease.Normalize()
			fmt.Fprintf(&b, "- Specialist: %s\n", lease.Specialist)
			fmt.Fprintf(&b, "  Root: %s\n", lease.Root)
			if strings.TrimSpace(lease.Branch) != "" {
				fmt.Fprintf(&b, "  Branch: %s\n", lease.Branch)
			}
			fmt.Fprintf(&b, "  Managed: %t\n", lease.Managed)
			if len(lease.OwnershipPaths) > 0 {
				fmt.Fprintf(&b, "  Ownership: %s\n", strings.Join(lease.OwnershipPaths, ", "))
			}
			if len(lease.NodeIDs) > 0 {
				fmt.Fprintf(&b, "  Nodes: %s\n", strings.Join(lease.NodeIDs, ", "))
			}
		}
		b.WriteString("\n")
	}
	if strings.TrimSpace(s.Summary) != "" {
		b.WriteString("## Summary\n\n")
		b.WriteString(s.Summary)
		b.WriteString("\n\n")
	}
	if len(s.Plan) > 0 {
		b.WriteString("## Plan\n\n")
		for _, item := range s.Plan {
			fmt.Fprintf(&b, "- [%s] %s\n", item.Status, item.Step)
		}
		b.WriteString("\n")
	}
	if strings.TrimSpace(s.ActiveFeatureID) != "" {
		b.WriteString("## Active Feature\n\n")
		fmt.Fprintf(&b, "- Active feature: %s\n", s.ActiveFeatureID)
		if strings.TrimSpace(s.LastFeatureID) != "" && !strings.EqualFold(s.LastFeatureID, s.ActiveFeatureID) {
			fmt.Fprintf(&b, "- Last feature: %s\n", s.LastFeatureID)
		}
		b.WriteString("\n")
	}
	if s.LastAnalysis != nil {
		b.WriteString("## Last Analysis\n\n")
		fmt.Fprintf(&b, "- Run ID: %s\n", s.LastAnalysis.RunID)
		fmt.Fprintf(&b, "- Goal: %s\n", s.LastAnalysis.Goal)
		if strings.TrimSpace(s.LastAnalysis.Mode) != "" {
			fmt.Fprintf(&b, "- Mode: %s\n", s.LastAnalysis.Mode)
		}
		fmt.Fprintf(&b, "- Status: %s\n", s.LastAnalysis.Status)
		fmt.Fprintf(&b, "- Agents: %d\n", s.LastAnalysis.AgentCount)
		if strings.TrimSpace(s.LastAnalysis.OutputPath) != "" {
			fmt.Fprintf(&b, "- Output: %s\n", s.LastAnalysis.OutputPath)
		}
		b.WriteString("\n")
	}
	if s.LastVerification != nil {
		b.WriteString("## Last Verification\n\n")
		b.WriteString(s.LastVerification.RenderDetailed())
		b.WriteString("\n\n")
	}
	if s.AcceptanceContract != nil {
		if rendered := strings.TrimSpace(s.AcceptanceContract.RenderPromptSection()); rendered != "" {
			b.WriteString("## Acceptance Contract\n\n")
			b.WriteString(rendered)
			b.WriteString("\n\n")
		}
	}
	if s.ActivePatchTransaction != nil {
		if rendered := strings.TrimSpace(s.ActivePatchTransaction.RenderPromptSection()); rendered != "" {
			b.WriteString("## Active Patch Transaction\n\n")
			b.WriteString(rendered)
			b.WriteString("\n\n")
		}
	}
	if s.ActiveEditLoop != nil {
		if rendered := strings.TrimSpace(s.ActiveEditLoop.RenderPromptSection()); rendered != "" {
			b.WriteString("## Active Edit Loop\n\n")
			b.WriteString(rendered)
			b.WriteString("\n\n")
		}
	}
	if len(s.EditLoops) > 0 {
		b.WriteString("## Recent Edit Loops\n\n")
		for _, loop := range s.EditLoops {
			if rendered := strings.TrimSpace(loop.RenderPromptSection()); rendered != "" {
				b.WriteString(rendered)
				b.WriteString("\n\n")
			}
		}
	}
	if s.LastCodingHarnessReport != nil {
		if rendered := strings.TrimSpace(s.LastCodingHarnessReport.RenderPromptSection()); rendered != "" {
			b.WriteString("## Last Coding Harness Report\n\n")
			b.WriteString(rendered)
			b.WriteString("\n\n")
		}
	}
	if s.LastUserChangeIsolationReport != nil {
		if rendered := strings.TrimSpace(s.LastUserChangeIsolationReport.RenderPromptSection()); rendered != "" {
			b.WriteString("## Last User Change Isolation Report\n\n")
			b.WriteString(rendered)
			b.WriteString("\n\n")
		}
	}
	if s.LastTestImpactReport != nil {
		if rendered := strings.TrimSpace(s.LastTestImpactReport.RenderPromptSection()); rendered != "" {
			b.WriteString("## Last Test Impact Report\n\n")
			b.WriteString(rendered)
			b.WriteString("\n\n")
		}
	}
	if s.LastJobSupervisorReport != nil {
		if rendered := strings.TrimSpace(s.LastJobSupervisorReport.RenderPromptSection()); rendered != "" {
			b.WriteString("## Last Job Supervisor Report\n\n")
			b.WriteString(rendered)
			b.WriteString("\n\n")
		}
	}
	if s.RuntimeGateLedger != nil {
		if rendered := strings.TrimSpace(s.RuntimeGateLedger.RenderPromptSection()); rendered != "" {
			b.WriteString("## Runtime Gate Ledger\n\n")
			b.WriteString(rendered)
			b.WriteString("\n\n")
		}
	}
	if s.ActiveFailureRepair != nil {
		if rendered := strings.TrimSpace(s.ActiveFailureRepair.RenderPromptSection()); rendered != "" {
			b.WriteString("## Active Failure Repair\n\n")
			b.WriteString(rendered)
			b.WriteString("\n\n")
		}
	}
	if s.TaskState != nil {
		if rendered := strings.TrimSpace(s.TaskState.RenderExportSection()); rendered != "" {
			b.WriteString("## Task State\n\n")
			b.WriteString(rendered)
			b.WriteString("\n\n")
		}
	}
	if s.TaskGraph != nil {
		if rendered := strings.TrimSpace(s.TaskGraph.RenderExportSection()); rendered != "" {
			b.WriteString("## Task Graph\n\n")
			b.WriteString(rendered)
			b.WriteString("\n\n")
		}
	}
	if rendered := renderBackgroundJobsExport(s.BackgroundJobs); rendered != "" {
		b.WriteString("## Background Jobs\n\n")
		b.WriteString(rendered)
		b.WriteString("\n\n")
	}
	if rendered := renderBackgroundBundlesExport(s.BackgroundBundles); rendered != "" {
		b.WriteString("## Background Bundles\n\n")
		b.WriteString(rendered)
		b.WriteString("\n\n")
	}
	if s.LastSelection != nil && s.LastSelection.HasSelection() {
		b.WriteString("## Last Selection\n\n")
		fmt.Fprintf(&b, "- %s\n\n", s.LastSelection.Summary(s.WorkingDir))
	}
	if len(s.Selections) > 0 {
		b.WriteString("## Selection Stack\n\n")
		for i, selection := range s.Selections {
			active := ""
			if i == s.ActiveSelection {
				active = " [active]"
			}
			fmt.Fprintf(&b, "- %d. %s%s\n", i+1, selection.Summary(s.WorkingDir), active)
		}
		b.WriteString("\n")
	}
	b.WriteString("## Transcript\n\n")
	for _, msg := range s.Messages {
		fmt.Fprintf(&b, "### %s\n\n", strings.ToUpper(msg.Role))
		if msg.ToolName != "" {
			fmt.Fprintf(&b, "Tool: %s (%s)\n\n", msg.ToolName, msg.ToolCallID)
		}
		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				fmt.Fprintf(&b, "- tool_call: %s %s\n", tc.Name, tc.Arguments)
			}
			b.WriteString("\n")
		}
		if len(msg.Images) > 0 {
			for _, image := range msg.Images {
				detail := strings.TrimSpace(image.Detail)
				metadata := ""
				if id := strings.TrimSpace(image.ID); id != "" {
					metadata += ", id=" + id
				}
				if status := strings.TrimSpace(image.Status); status != "" {
					metadata += ", status=" + status
				}
				if revisedPrompt := strings.TrimSpace(image.RevisedPrompt); revisedPrompt != "" {
					metadata += ", revised_prompt=" + compactPromptSection(revisedPrompt, 120)
				}
				if detail != "" {
					fmt.Fprintf(&b, "- image: %s (%s, detail=%s%s)\n", image.Path, image.MediaType, detail, metadata)
				} else {
					fmt.Fprintf(&b, "- image: %s (%s%s)\n", image.Path, image.MediaType, metadata)
				}
			}
			b.WriteString("\n")
		}
		if len(msg.WebSearchCalls) > 0 {
			for _, call := range msg.WebSearchCalls {
				id := strings.TrimSpace(call.ID)
				if id != "" {
					id = " id=" + id
				}
				action := ""
				if encoded, err := json.Marshal(call.Action); err == nil && len(encoded) > 0 && string(encoded) != "null" {
					action = " " + string(encoded)
				}
				fmt.Fprintf(&b, "- web_search:%s %s%s\n", id, strings.TrimSpace(call.Status), action)
			}
			b.WriteString("\n")
		}
		if len(msg.LocalShellCalls) > 0 {
			for _, call := range msg.LocalShellCalls {
				action := ""
				if encoded, err := json.Marshal(call.Action); err == nil && len(encoded) > 0 && string(encoded) != "null" {
					action = " " + string(encoded)
				}
				callID := strings.TrimSpace(call.CallID)
				if callID == "" {
					callID = strings.TrimSpace(call.ID)
				}
				fmt.Fprintf(&b, "- local_shell_call: %s %s%s\n", strings.TrimSpace(callID), strings.TrimSpace(call.Status), action)
			}
			b.WriteString("\n")
		}
		if len(msg.CodexCompactionItems) > 0 {
			for _, item := range msg.CodexCompactionItems {
				if strings.TrimSpace(item.EncryptedContent) != "" {
					fmt.Fprintf(&b, "- codex_%s: encrypted content present (%d bytes)\n", strings.TrimSpace(item.Type), len(item.EncryptedContent))
				} else {
					fmt.Fprintf(&b, "- codex_%s\n", strings.TrimSpace(item.Type))
				}
			}
			b.WriteString("\n")
		}
		if len(msg.CodexToolOutputItems) > 0 {
			for _, item := range msg.CodexToolOutputItems {
				callID := strings.TrimSpace(item.CallID)
				detail := strings.TrimSpace(item.Text)
				if detail == "" && len(item.ToolContentItems) > 0 {
					detail = fmt.Sprintf("%d content item(s)", len(item.ToolContentItems))
				}
				if detail == "" && len(item.Tools) > 0 {
					detail = fmt.Sprintf("%d tool(s)", len(item.Tools))
				}
				if detail != "" {
					fmt.Fprintf(&b, "- codex_%s: %s %s\n", strings.TrimSpace(item.Type), callID, compactPromptSection(detail, 160))
				} else {
					fmt.Fprintf(&b, "- codex_%s: %s\n", strings.TrimSpace(item.Type), callID)
				}
			}
			b.WriteString("\n")
		}
		if strings.TrimSpace(msg.ReasoningEncryptedContent) != "" {
			fmt.Fprintf(&b, "- reasoning encrypted content: present (%d bytes)\n\n", len(msg.ReasoningEncryptedContent))
		}
		if msg.Text != "" {
			b.WriteString(msg.Text)
			if !strings.HasSuffix(msg.Text, "\n") {
				b.WriteByte('\n')
			}
			b.WriteByte('\n')
		}
	}
	return b.String()
}

type SessionSummary struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	WorkingDir string    `json:"working_dir"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type SessionSearchResult struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	WorkingDir string    `json:"working_dir"`
	UpdatedAt  time.Time `json:"updated_at"`
	MatchField string    `json:"match_field"`
	Snippet    string    `json:"snippet"`
}

type SessionStore struct {
	root string
}

func NewSessionStore(root string) *SessionStore {
	return &SessionStore{root: root}
}

func (s *SessionStore) Root() string {
	return s.root
}

func (s *SessionStore) Save(sess *Session) error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return err
	}
	sess.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	path := s.sessionPath(sess.ID)
	unlock := lockFilePath(path)
	defer unlock()
	if existing, err := os.ReadFile(path); err == nil && json.Valid(existing) {
		_ = atomicWriteFile(sessionBackupPath(path), existing, 0o644)
	}
	if err := atomicWriteFile(path, data, 0o644); err != nil {
		return err
	}
	_ = atomicWriteFile(sessionBackupPath(path), data, 0o644)
	return nil
}

func (s *SessionStore) Load(id string) (*Session, error) {
	path := s.sessionPath(id)
	unlock := lockFilePath(path)
	defer unlock()
	sess, _, err := loadSessionFile(path)
	if err == nil {
		return sess, nil
	}
	primaryErr := err
	backupPath := sessionBackupPath(path)
	backupSess, backupData, backupErr := loadSessionFile(backupPath)
	if backupErr != nil {
		return nil, primaryErr
	}
	_ = atomicWriteFile(path, backupData, 0o644)
	return backupSess, nil
}

func (s *SessionStore) sessionPath(id string) string {
	return filepath.Join(s.root, id+".json")
}

func sessionBackupPath(path string) string {
	return path + ".bak"
}

func loadSessionFile(path string) (*Session, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, nil, err
	}
	sess.normalizeWorkingDirState()
	sess.normalizeSelectionState()
	sess.normalizeTaskStateArtifacts()
	sess.normalizeCodingHarnessState()
	sess.normalizeFailureRepairState()
	sess.normalizeEditLoopState()
	sess.normalizeBackgroundJobs()
	sess.normalizeBackgroundBundles()
	sess.normalizeSpecialistWorktrees()
	sess.normalizeConversationRuntime()
	sess.normalizeSuggestionMemory()
	sess.normalizeAutomations()
	sess.normalizeGoals()
	return &sess, data, nil
}

func loadSessionFileWithBackup(path string) (*Session, error) {
	sess, _, err := loadSessionFile(path)
	if err == nil {
		return sess, nil
	}
	sess, _, err = loadSessionFile(sessionBackupPath(path))
	if err != nil {
		return nil, err
	}
	return sess, nil
}

func (s *SessionStore) List() ([]SessionSummary, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	items := make([]SessionSummary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(s.root, entry.Name())
		sess, err := loadSessionFileWithBackup(path)
		if err != nil {
			continue
		}
		items = append(items, SessionSummary{
			ID:         sess.ID,
			Name:       sess.Name,
			WorkingDir: sess.WorkingDir,
			UpdatedAt:  sess.UpdatedAt,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return items, nil
}

func (s *SessionStore) Search(query string, limit int) ([]SessionSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("session search requires a query")
	}
	if limit <= 0 {
		limit = 20
	}

	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	results := make([]SessionSearchResult, 0, min(limit, len(entries)))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(s.root, entry.Name())
		sess, err := loadSessionFileWithBackup(path)
		if err != nil {
			continue
		}
		field, snippet, ok := sessionSearchMatch(sess, query)
		if !ok {
			continue
		}
		results = append(results, SessionSearchResult{
			ID:         sess.ID,
			Name:       sess.Name,
			WorkingDir: sess.WorkingDir,
			UpdatedAt:  sess.UpdatedAt,
			MatchField: field,
			Snippet:    snippet,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].UpdatedAt.After(results[j].UpdatedAt)
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

type sessionSearchField struct {
	name string
	text string
}

func sessionSearchMatch(sess *Session, query string) (string, string, bool) {
	fields := []sessionSearchField{
		{name: "name", text: sess.Name},
		{name: "id", text: sess.ID},
		{name: "working_dir", text: sess.WorkingDir},
		{name: "summary", text: sess.Summary},
		{name: "transcript", text: sess.ExportText()},
	}
	for _, field := range fields {
		snippet, ok := sessionSearchSnippet(field.text, query)
		if ok {
			return field.name, snippet, true
		}
	}
	return "", "", false
}

func sessionSearchSnippet(text string, query string) (string, bool) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return "", false
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		matchRune := caseInsensitiveRuneIndex(line, query)
		if matchRune < 0 {
			continue
		}
		return sessionSearchExcerpt(line, matchRune, 180), true
	}
	return "", false
}

func sessionSearchExcerpt(line string, matchRune int, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = 180
	}
	runes := []rune(line)
	if len(runes) <= maxRunes {
		return string(runes)
	}
	if matchRune < 0 {
		matchRune = 0
	}
	before := maxRunes / 3
	start := matchRune - before
	if start < 0 {
		start = 0
	}
	if start+maxRunes > len(runes) {
		start = len(runes) - maxRunes
	}
	end := start + maxRunes
	snippet := strings.TrimSpace(string(runes[start:end]))
	if start > 0 {
		snippet = "... " + snippet
	}
	if end < len(runes) {
		snippet += " ..."
	}
	return snippet
}

func caseInsensitiveRuneIndex(text string, query string) int {
	query = strings.TrimSpace(query)
	if query == "" {
		return -1
	}
	textRunes := []rune(text)
	queryRunes := []rune(query)
	if len(queryRunes) == 0 || len(queryRunes) > len(textRunes) {
		return -1
	}
	for i := 0; i+len(queryRunes) <= len(textRunes); i++ {
		if strings.EqualFold(string(textRunes[i:i+len(queryRunes)]), query) {
			return i
		}
	}
	return -1
}

func (s *Session) normalizeSelectionState() {
	if len(s.Selections) == 0 {
		if s.LastSelection != nil && s.LastSelection.HasSelection() {
			s.Selections = []ViewerSelection{*s.LastSelection}
			s.ActiveSelection = 0
		}
		return
	}
	filtered := make([]ViewerSelection, 0, len(s.Selections))
	for _, selection := range s.Selections {
		if selection.HasSelection() {
			filtered = append(filtered, selection)
		}
	}
	s.Selections = filtered
	if len(s.Selections) == 0 {
		s.LastSelection = nil
		s.ActiveSelection = 0
		return
	}
	if s.ActiveSelection < 0 || s.ActiveSelection >= len(s.Selections) {
		s.ActiveSelection = len(s.Selections) - 1
	}
	active := s.Selections[s.ActiveSelection]
	s.LastSelection = &active
}

func (s *Session) normalizeWorkingDirState() {
	if s == nil {
		return
	}
	s.BaseWorkingDir = strings.TrimSpace(s.BaseWorkingDir)
	s.WorkingDir = strings.TrimSpace(s.WorkingDir)
	if s.BaseWorkingDir == "" {
		s.BaseWorkingDir = s.WorkingDir
	}
	if s.WorkingDir == "" {
		s.WorkingDir = s.BaseWorkingDir
	}
	if s.Worktree == nil {
		return
	}
	s.Worktree.Normalize()
	if s.Worktree.Active && strings.TrimSpace(s.Worktree.Root) != "" &&
		(s.WorkingDir == "" || strings.EqualFold(strings.TrimSpace(s.WorkingDir), strings.TrimSpace(s.BaseWorkingDir))) {
		s.WorkingDir = s.Worktree.Root
	}
	if !s.Worktree.Active && s.BaseWorkingDir != "" {
		s.WorkingDir = s.BaseWorkingDir
	}
	s.normalizeSpecialistWorktrees()
}

func (s *Session) CurrentSelection() *ViewerSelection {
	s.normalizeSelectionState()
	if len(s.Selections) == 0 {
		return nil
	}
	selection := s.Selections[s.ActiveSelection]
	return &selection
}

func (s *Session) AddSelection(selection ViewerSelection) {
	if !selection.HasSelection() {
		return
	}
	s.normalizeSelectionState()
	for i, existing := range s.Selections {
		if strings.EqualFold(existing.FilePath, selection.FilePath) && existing.StartLine == selection.StartLine && existing.EndLine == selection.EndLine {
			if strings.TrimSpace(selection.Note) != "" {
				s.Selections[i].Note = selection.Note
			}
			if len(selection.Tags) > 0 {
				s.Selections[i].Tags = uniqueStrings(selection.Tags)
			}
			s.ActiveSelection = i
			s.LastSelection = &s.Selections[i]
			return
		}
	}
	s.Selections = append(s.Selections, selection)
	s.ActiveSelection = len(s.Selections) - 1
	active := s.Selections[s.ActiveSelection]
	s.LastSelection = &active
}

func (s *Session) SetActiveSelection(index int) bool {
	s.normalizeSelectionState()
	if index < 0 || index >= len(s.Selections) {
		return false
	}
	s.ActiveSelection = index
	active := s.Selections[index]
	s.LastSelection = &active
	return true
}

func (s *Session) RemoveSelection(index int) bool {
	s.normalizeSelectionState()
	if index < 0 || index >= len(s.Selections) {
		return false
	}
	s.Selections = append(s.Selections[:index], s.Selections[index+1:]...)
	if len(s.Selections) == 0 {
		s.LastSelection = nil
		s.ActiveSelection = 0
		return true
	}
	if s.ActiveSelection >= len(s.Selections) {
		s.ActiveSelection = len(s.Selections) - 1
	}
	active := s.Selections[s.ActiveSelection]
	s.LastSelection = &active
	return true
}

func (s *Session) ClearSelections() {
	s.Selections = nil
	s.ActiveSelection = 0
	s.LastSelection = nil
}

func (s *Session) SelectionAt(index int) (*ViewerSelection, bool) {
	s.normalizeSelectionState()
	if index < 0 || index >= len(s.Selections) {
		return nil, false
	}
	selection := s.Selections[index]
	return &selection, true
}

func (s *Session) SpecialistWorktree(name string) (SpecialistWorktree, bool) {
	if s == nil {
		return SpecialistWorktree{}, false
	}
	for _, lease := range s.SpecialistWorktrees {
		if strings.EqualFold(strings.TrimSpace(lease.Specialist), strings.TrimSpace(name)) {
			return lease, true
		}
	}
	return SpecialistWorktree{}, false
}

func (s *Session) UpsertSpecialistWorktree(lease SpecialistWorktree) {
	if s == nil {
		return
	}
	lease.Normalize()
	if strings.TrimSpace(lease.Specialist) == "" {
		return
	}
	for i := range s.SpecialistWorktrees {
		if strings.EqualFold(strings.TrimSpace(s.SpecialistWorktrees[i].Specialist), strings.TrimSpace(lease.Specialist)) {
			s.SpecialistWorktrees[i] = lease
			s.normalizeSpecialistWorktrees()
			return
		}
	}
	s.SpecialistWorktrees = append(s.SpecialistWorktrees, lease)
	s.normalizeSpecialistWorktrees()
}

func (s *Session) RemoveSpecialistWorktree(name string) bool {
	if s == nil {
		return false
	}
	target := strings.TrimSpace(name)
	if target == "" {
		return false
	}
	for i := range s.SpecialistWorktrees {
		if !strings.EqualFold(strings.TrimSpace(s.SpecialistWorktrees[i].Specialist), target) {
			continue
		}
		s.SpecialistWorktrees = append(s.SpecialistWorktrees[:i], s.SpecialistWorktrees[i+1:]...)
		s.normalizeSpecialistWorktrees()
		return true
	}
	return false
}

func (s *Session) normalizeSpecialistWorktrees() {
	if s == nil || len(s.SpecialistWorktrees) == 0 {
		return
	}
	for i := range s.SpecialistWorktrees {
		s.SpecialistWorktrees[i].Normalize()
	}
	sort.Slice(s.SpecialistWorktrees, func(i, j int) bool {
		left := s.SpecialistWorktrees[i]
		right := s.SpecialistWorktrees[j]
		if left.UpdatedAt.Equal(right.UpdatedAt) {
			return strings.Compare(strings.ToLower(left.Specialist), strings.ToLower(right.Specialist)) < 0
		}
		return left.UpdatedAt.After(right.UpdatedAt)
	})
}
