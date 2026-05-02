package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type DelegationHandoffArtifact struct {
	ID              string    `json:"id"`
	CreatedAt       time.Time `json:"created_at"`
	SessionID       string    `json:"session_id,omitempty"`
	Workspace       string    `json:"workspace,omitempty"`
	Branch          string    `json:"branch,omitempty"`
	Provider        string    `json:"provider,omitempty"`
	Model           string    `json:"model,omitempty"`
	Note            string    `json:"note,omitempty"`
	ChangedFiles    []string  `json:"changed_files,omitempty"`
	OpenTasks       []string  `json:"open_tasks,omitempty"`
	Verification    string    `json:"verification,omitempty"`
	RecentEvents    []string  `json:"recent_events,omitempty"`
	ArtifactRefs    []string  `json:"artifact_refs,omitempty"`
	SuggestedPrompt string    `json:"suggested_prompt,omitempty"`
}

type DelegationImportArtifact struct {
	ID            string    `json:"id"`
	ImportedAt    time.Time `json:"imported_at"`
	SourcePath    string    `json:"source_path,omitempty"`
	SourceID      string    `json:"source_id,omitempty"`
	SourceSession string    `json:"source_session,omitempty"`
	Status        string    `json:"status,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	Verification  string    `json:"verification,omitempty"`
	ChangedFiles  []string  `json:"changed_files,omitempty"`
	CompletedTask []string  `json:"completed_tasks,omitempty"`
	OpenTasks     []string  `json:"open_tasks,omitempty"`
	ArtifactRefs  []string  `json:"artifact_refs,omitempty"`
	Notes         []string  `json:"notes,omitempty"`
}

func (rt *runtimeState) handleDelegationHandoffCommand(args string) error {
	if rt == nil || rt.session == nil {
		return fmt.Errorf("no active session")
	}
	root := workspaceSnapshotRoot(rt.workspace)
	if strings.TrimSpace(root) == "" {
		root = rt.session.WorkingDir
	}
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("workspace root is not configured")
	}
	fields := splitAnalysisCommandLine(strings.TrimSpace(args))
	if len(fields) > 0 && strings.EqualFold(fields[0], "import") {
		if len(fields) < 2 {
			return fmt.Errorf("usage: /handoff import <artifact.json|result.md>")
		}
		return rt.handleDelegationImportCommand(root, fields[1])
	}
	artifact := rt.buildDelegationHandoffArtifact(root, args)
	outDir := filepath.Join(root, ".kernforge", "handoff")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	jsonPath := filepath.Join(outDir, "latest.json")
	mdPath := filepath.Join(outDir, "latest.md")
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(mdPath, []byte(renderDelegationHandoffMarkdown(artifact)), 0o644); err != nil {
		return err
	}
	rt.session.AppendConversationEvent(ConversationEvent{
		Kind:     conversationEventKindHandoff,
		Severity: conversationSeverityInfo,
		Summary:  "delegation handoff artifact generated",
		ArtifactRefs: []string{
			mdPath,
			jsonPath,
		},
		Entities: map[string]string{
			"handoff": artifact.ID,
		},
	})
	if rt.store != nil {
		if err := rt.store.Save(rt.session); err != nil {
			return err
		}
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("Generated delegation handoff: "+mdPath))
	return nil
}

func (rt *runtimeState) handleDelegationImportCommand(root string, rawPath string) error {
	sourcePath := strings.TrimSpace(rawPath)
	if sourcePath == "" {
		return fmt.Errorf("handoff import path is empty")
	}
	if !filepath.IsAbs(sourcePath) {
		sourcePath = filepath.Join(root, sourcePath)
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	imported := parseDelegationImportArtifact(sourcePath, data)
	imported.ImportedAt = time.Now()
	if strings.TrimSpace(imported.ID) == "" {
		imported.ID = "handoff-import-" + imported.ImportedAt.Format("20060102-150405")
	}
	imported.SourcePath = sourcePath
	imported.ArtifactRefs = analysisUniqueStrings(append([]string{sourcePath}, imported.ArtifactRefs...))
	completed := markDelegationImportedTasks(rt.session, imported.CompletedTask, imported.Summary)
	outDir := filepath.Join(root, ".kernforge", "handoff", "imports")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	stem := sanitizeFileName(imported.ID)
	if strings.TrimSpace(stem) == "" {
		stem = "handoff_import_" + shortStableID(sourcePath)
	}
	jsonPath := filepath.Join(outDir, stem+".json")
	mdPath := filepath.Join(outDir, stem+".md")
	encoded, err := json.MarshalIndent(imported, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, encoded, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(mdPath, []byte(renderDelegationImportMarkdown(imported, completed)), 0o644); err != nil {
		return err
	}
	rt.session.AppendConversationEvent(ConversationEvent{
		Kind:         conversationEventKindHandoff,
		Severity:     delegationImportSeverity(imported),
		Summary:      "delegation result imported: " + valueOrDefault(imported.Summary, imported.ID),
		ArtifactRefs: analysisUniqueStrings(append([]string{mdPath, jsonPath}, imported.ArtifactRefs...)),
		Entities: map[string]string{
			"handoff_import":  imported.ID,
			"source":          sourcePath,
			"status":          imported.Status,
			"completed_tasks": strings.Join(imported.CompletedTask, ","),
			"tasks_marked":    fmt.Sprintf("%d", completed),
		},
	})
	if rt.store != nil {
		if err := rt.store.Save(rt.session); err != nil {
			return err
		}
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Imported delegation result: %s (tasks_marked=%d)", mdPath, completed)))
	return nil
}

func (rt *runtimeState) buildDelegationHandoffArtifact(root string, note string) DelegationHandoffArtifact {
	now := time.Now()
	artifact := DelegationHandoffArtifact{
		ID:        fmt.Sprintf("handoff-%s", now.Format("20060102-150405")),
		CreatedAt: now,
		SessionID: rt.session.ID,
		Workspace: root,
		Branch:    delegationGitBranch(root),
		Provider:  rt.session.Provider,
		Model:     rt.session.Model,
		Note:      strings.TrimSpace(note),
	}
	artifact.ChangedFiles = delegationChangedFiles(root)
	artifact.OpenTasks = delegationOpenTasks(rt.session)
	if rt.session.LastVerification != nil {
		artifact.Verification = rt.session.LastVerification.SummaryLine()
	}
	artifact.RecentEvents = delegationRecentEvents(rt.session.ConversationEvents, 8)
	artifact.ArtifactRefs = delegationArtifactRefs(rt.session.ConversationEvents, 12)
	artifact.SuggestedPrompt = delegationSuggestedPrompt(artifact)
	return artifact
}

func parseDelegationImportArtifact(sourcePath string, data []byte) DelegationImportArtifact {
	imported := DelegationImportArtifact{}
	if err := json.Unmarshal(data, &imported); err == nil && delegationImportHasContent(imported) {
		imported.normalize()
		return imported
	}
	handoff := DelegationHandoffArtifact{}
	if err := json.Unmarshal(data, &handoff); err == nil && strings.TrimSpace(handoff.ID) != "" {
		imported = DelegationImportArtifact{
			ID:            "import-" + handoff.ID,
			SourceID:      handoff.ID,
			SourceSession: handoff.SessionID,
			Status:        "handoff",
			Summary:       valueOrDefault(handoff.Note, "Imported handoff artifact"),
			Verification:  handoff.Verification,
			ChangedFiles:  handoff.ChangedFiles,
			OpenTasks:     handoff.OpenTasks,
			ArtifactRefs:  handoff.ArtifactRefs,
			Notes:         normalizeTaskStateList([]string{handoff.Note, handoff.SuggestedPrompt}, 8),
		}
		imported.normalize()
		return imported
	}
	text := strings.TrimSpace(string(data))
	imported = DelegationImportArtifact{
		ID:      "handoff-import-" + shortStableID(sourcePath+":"+text),
		Status:  "imported",
		Summary: firstNonEmptyLine(text),
		Notes:   normalizeTaskStateList(strings.Split(text, "\n"), 12),
	}
	imported.normalize()
	return imported
}

func delegationImportHasContent(imported DelegationImportArtifact) bool {
	return strings.TrimSpace(imported.ID) != "" ||
		strings.TrimSpace(imported.Summary) != "" ||
		strings.TrimSpace(imported.Status) != "" ||
		len(imported.ArtifactRefs) > 0 ||
		len(imported.CompletedTask) > 0
}

func (a *DelegationImportArtifact) normalize() {
	if a == nil {
		return
	}
	a.ID = strings.TrimSpace(a.ID)
	a.SourcePath = strings.TrimSpace(a.SourcePath)
	a.SourceID = strings.TrimSpace(a.SourceID)
	a.SourceSession = strings.TrimSpace(a.SourceSession)
	a.Status = strings.TrimSpace(strings.ToLower(a.Status))
	a.Summary = compactPromptSection(strings.TrimSpace(a.Summary), 300)
	a.Verification = compactPromptSection(strings.TrimSpace(a.Verification), 400)
	a.ChangedFiles = normalizeTaskStateList(a.ChangedFiles, 64)
	a.CompletedTask = normalizeTaskStateList(a.CompletedTask, 64)
	a.OpenTasks = normalizeTaskStateList(a.OpenTasks, 64)
	a.ArtifactRefs = normalizeTaskStateList(a.ArtifactRefs, 64)
	a.Notes = normalizeTaskStateList(a.Notes, 24)
	if a.Status == "" {
		a.Status = "imported"
	}
	if a.Summary == "" {
		a.Summary = valueOrDefault(a.SourceID, "Imported delegation result")
	}
}

func markDelegationImportedTasks(session *Session, taskIDs []string, note string) int {
	if session == nil || len(taskIDs) == 0 {
		return 0
	}
	count := 0
	for _, taskID := range taskIDs {
		taskID = strings.TrimSpace(taskID)
		if taskID == "" {
			continue
		}
		if session.SetPlanNodeLifecycle(taskID, "completed", valueOrDefault(note, "Completed by imported delegation result")) {
			count++
		}
	}
	return count
}

func delegationImportSeverity(imported DelegationImportArtifact) string {
	switch strings.TrimSpace(strings.ToLower(imported.Status)) {
	case "failed", "failure", "blocked", "error":
		return conversationSeverityWarn
	default:
		return conversationSeverityInfo
	}
}

func delegationGitBranch(root string) string {
	branch := strings.TrimSpace(runGitText(root, "rev-parse", "--abbrev-ref", "HEAD"))
	if branch == "" || strings.Contains(strings.ToLower(branch), "fatal:") {
		return "unknown"
	}
	return branch
}

func delegationChangedFiles(root string) []string {
	status := runGitText(root, "status", "--short")
	diffName := runGitText(root, "diff", "--name-only")
	files := prReviewChangedFiles(status + "\n" + diffName)
	out := make([]string, 0, len(files))
	for _, file := range files {
		lower := strings.ToLower(strings.TrimSpace(file))
		if strings.Contains(lower, "fatal:") || strings.HasPrefix(lower, "exit status ") {
			continue
		}
		out = append(out, file)
	}
	return analysisUniqueStrings(out)
}

func delegationOpenTasks(session *Session) []string {
	if session == nil || session.TaskGraph == nil {
		return nil
	}
	session.TaskGraph.Normalize()
	out := []string{}
	for _, node := range session.TaskGraph.Nodes {
		status := strings.TrimSpace(strings.ToLower(node.Status))
		switch status {
		case "completed", "canceled", "superseded", "preempted":
			continue
		}
		out = append(out, fmt.Sprintf("%s [%s]: %s", node.ID, valueOrDefault(node.Status, "pending"), node.Title))
		if len(out) >= 16 {
			break
		}
	}
	return out
}

func delegationRecentEvents(events []ConversationEvent, limit int) []string {
	if limit <= 0 || len(events) == 0 {
		return nil
	}
	start := len(events) - limit
	if start < 0 {
		start = 0
	}
	out := []string{}
	for _, event := range events[start:] {
		text := strings.TrimSpace(fmt.Sprintf("%s %s %s", event.Kind, valueOrDefault(event.Severity, "info"), event.Summary))
		if text != "" {
			out = append(out, compactPromptSection(text, 220))
		}
	}
	return out
}

func delegationArtifactRefs(events []ConversationEvent, limit int) []string {
	refs := []string{}
	for index := len(events) - 1; index >= 0 && len(refs) < limit; index-- {
		for _, ref := range events[index].ArtifactRefs {
			ref = strings.TrimSpace(ref)
			if ref != "" {
				refs = append(refs, ref)
				if len(refs) >= limit {
					break
				}
			}
		}
	}
	return analysisUniqueStrings(refs)
}

func delegationSuggestedPrompt(artifact DelegationHandoffArtifact) string {
	var b strings.Builder
	b.WriteString("Continue this Kernforge task from the handoff artifact. ")
	if strings.TrimSpace(artifact.Note) != "" {
		b.WriteString("Goal: ")
		b.WriteString(artifact.Note)
		b.WriteString(". ")
	}
	b.WriteString("First inspect changed files and open tasks, preserve user changes, run focused verification, and report remaining risks.")
	return strings.TrimSpace(b.String())
}

func renderDelegationHandoffMarkdown(artifact DelegationHandoffArtifact) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Delegation Handoff\n\n")
	fmt.Fprintf(&b, "ID: %s\n", artifact.ID)
	fmt.Fprintf(&b, "Generated: %s\n", artifact.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "Workspace: %s\n", artifact.Workspace)
	fmt.Fprintf(&b, "Branch: %s\n", valueOrDefault(artifact.Branch, "unknown"))
	fmt.Fprintf(&b, "Session: %s\n", artifact.SessionID)
	fmt.Fprintf(&b, "Provider/model: %s / %s\n", valueOrUnset(artifact.Provider), valueOrUnset(artifact.Model))
	if strings.TrimSpace(artifact.Note) != "" {
		fmt.Fprintf(&b, "\n## Goal\n\n%s\n", artifact.Note)
	}
	writeDelegationList(&b, "Changed Files", artifact.ChangedFiles, "No changed files detected.")
	writeDelegationList(&b, "Open Tasks", artifact.OpenTasks, "No open task graph nodes.")
	if strings.TrimSpace(artifact.Verification) != "" {
		fmt.Fprintf(&b, "\n## Verification\n\n%s\n", artifact.Verification)
	}
	writeDelegationList(&b, "Recent Events", artifact.RecentEvents, "No recent events recorded.")
	writeDelegationList(&b, "Artifact Refs", artifact.ArtifactRefs, "No artifact refs recorded.")
	fmt.Fprintf(&b, "\n## Suggested Prompt\n\n%s\n", artifact.SuggestedPrompt)
	return strings.TrimSpace(b.String())
}

func writeDelegationList(b *strings.Builder, title string, items []string, empty string) {
	fmt.Fprintf(b, "\n## %s\n\n", title)
	if len(items) == 0 {
		fmt.Fprintf(b, "- %s\n", empty)
		return
	}
	for _, item := range items {
		fmt.Fprintf(b, "- %s\n", item)
	}
}

func renderDelegationImportMarkdown(imported DelegationImportArtifact, tasksMarked int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Delegation Import\n\n")
	fmt.Fprintf(&b, "ID: %s\n", imported.ID)
	fmt.Fprintf(&b, "Imported: %s\n", imported.ImportedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "Source: %s\n", imported.SourcePath)
	fmt.Fprintf(&b, "Status: %s\n", valueOrUnset(imported.Status))
	fmt.Fprintf(&b, "Tasks marked: %d\n", tasksMarked)
	if strings.TrimSpace(imported.SourceID) != "" {
		fmt.Fprintf(&b, "Source ID: %s\n", imported.SourceID)
	}
	if strings.TrimSpace(imported.SourceSession) != "" {
		fmt.Fprintf(&b, "Source session: %s\n", imported.SourceSession)
	}
	fmt.Fprintf(&b, "\n## Summary\n\n%s\n", valueOrDefault(imported.Summary, "Imported delegation result"))
	if strings.TrimSpace(imported.Verification) != "" {
		fmt.Fprintf(&b, "\n## Verification\n\n%s\n", imported.Verification)
	}
	writeDelegationList(&b, "Completed Tasks", imported.CompletedTask, "No completed task IDs supplied.")
	writeDelegationList(&b, "Open Tasks", imported.OpenTasks, "No open tasks supplied.")
	writeDelegationList(&b, "Changed Files", imported.ChangedFiles, "No changed files supplied.")
	writeDelegationList(&b, "Artifact Refs", imported.ArtifactRefs, "No artifact refs supplied.")
	writeDelegationList(&b, "Notes", imported.Notes, "No notes supplied.")
	return strings.TrimSpace(b.String())
}
