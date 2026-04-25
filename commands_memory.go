package main

import (
	"fmt"
	"strings"
	"time"
)

func (rt *runtimeState) handlePersistentMemoryRecent(args string) error {
	if rt.longMem == nil {
		return fmt.Errorf("persistent memory is not configured")
	}
	if strings.TrimSpace(args) != "" {
		return rt.handlePersistentMemorySearch(args)
	}
	records, err := rt.longMem.ListRecent(rt.workspace.BaseRoot, 8)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No persistent memory records found for this workspace."))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Persistent Memory"))
	for _, record := range records {
		fmt.Fprintf(rt.writer, "%s  importance=%s  trust=%s  %s\n", rt.ui.dim(record.Citation()), record.ImportanceLabel(), record.TrustLabel(), compactPersistentMemoryText(record.Summary, 220))
	}
	if handoff := memoryHandoff(records); strings.TrimSpace(handoff) != "" {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, handoff)
	}
	return nil
}

func (rt *runtimeState) handlePersistentMemorySearch(query string) error {
	if rt.longMem == nil {
		return fmt.Errorf("persistent memory is not configured")
	}
	if strings.TrimSpace(query) == "" {
		return fmt.Errorf("usage: /mem-search <query>")
	}
	records, err := rt.longMem.SearchHits(query, rt.workspace.BaseRoot, rt.session.ID, 8)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No persistent memory matched that query."))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Memory Search"))
	for _, hit := range records {
		fmt.Fprintf(rt.writer, "%s  importance=%s  trust=%s  score=%d  %s\n", rt.ui.dim(hit.Citation), hit.Record.ImportanceLabel(), hit.Record.TrustLabel(), hit.Score, compactPersistentMemoryText(hit.Record.Summary, 260))
	}
	if handoff := memoryHandoffFromHits(records); strings.TrimSpace(handoff) != "" {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, handoff)
	}
	return nil
}

func (rt *runtimeState) handlePersistentMemoryShow(id string) error {
	if rt.longMem == nil {
		return fmt.Errorf("persistent memory is not configured")
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("usage: /mem-show <id>")
	}
	record, ok, err := rt.longMem.Get(id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("persistent memory record not found: %s", id)
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Memory Record"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("citation", record.Citation()))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("importance", record.ImportanceLabel()))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("trust", record.TrustLabel()))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("workspace", record.Workspace))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("request", valueOrUnset(record.Request)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("reply", valueOrUnset(record.Reply)))
	if strings.TrimSpace(record.VerificationSummary) != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("verification", record.VerificationSummary))
	}
	if len(record.VerificationCategories) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("verification_categories", strings.Join(record.VerificationCategories, ", ")))
	}
	if len(record.VerificationTags) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("verification_tags", strings.Join(record.VerificationTags, ", ")))
	}
	if len(record.VerificationArtifacts) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("verification_artifacts", strings.Join(record.VerificationArtifacts, ", ")))
	}
	if len(record.VerificationFailures) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("verification_failures", strings.Join(record.VerificationFailures, ", ")))
	}
	if len(record.VerificationSeverities) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("verification_severities", strings.Join(record.VerificationSeverities, ", ")))
	}
	if len(record.VerificationSignals) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("verification_signals", strings.Join(record.VerificationSignals, ", ")))
	}
	if record.VerificationMaxRisk > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("verification_max_risk", fmt.Sprintf("%d", record.VerificationMaxRisk)))
	}
	if len(record.Files) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("refs", strings.Join(record.Files, ", ")))
	}
	if len(record.ToolNames) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("tools", strings.Join(record.ToolNames, ", ")))
	}
	if handoff := memoryHandoff([]PersistentMemoryRecord{record}); strings.TrimSpace(handoff) != "" {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, handoff)
	}
	return nil
}

func (rt *runtimeState) handlePersistentMemoryAdjust(id, action string) error {
	if rt.longMem == nil {
		return fmt.Errorf("persistent memory is not configured")
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("usage: /mem-%s <id>", action)
	}
	var (
		record PersistentMemoryRecord
		ok     bool
		err    error
	)
	switch action {
	case "promote":
		record, ok, err = rt.longMem.Promote(id)
	case "demote":
		record, ok, err = rt.longMem.Demote(id)
	case "confirm":
		record, ok, err = rt.longMem.SetTrust(id, PersistentMemoryConfirmed)
	case "tentative":
		record, ok, err = rt.longMem.SetTrust(id, PersistentMemoryTentative)
	default:
		return fmt.Errorf("unsupported memory action: %s", action)
	}
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("persistent memory record not found: %s", id)
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Updated %s -> importance=%s trust=%s", record.Citation(), record.ImportanceLabel(), record.TrustLabel())))
	return nil
}

func (rt *runtimeState) handlePersistentMemoryDashboard(query string, html bool) error {
	if rt.longMem == nil {
		return fmt.Errorf("persistent memory is not configured")
	}
	summary, err := rt.longMem.Dashboard(workspaceSnapshotRoot(rt.workspace), query, 12)
	if err != nil {
		return err
	}
	if html {
		outputPath, err := createPersistentMemoryDashboardHTML(summary)
		if err != nil {
			return err
		}
		if err := OpenExternalURL(outputPath); err != nil {
			fmt.Fprintln(rt.writer, rt.ui.warnLine("Generated HTML memory dashboard but could not open it automatically: "+err.Error()))
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Generated memory dashboard: "+outputPath))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Memory Dashboard"))
	fmt.Fprintln(rt.writer, renderPersistentMemoryDashboard(summary))
	return nil
}

func (rt *runtimeState) handlePersistentMemoryPrune(args string) error {
	if rt.longMem == nil {
		return fmt.Errorf("persistent memory is not configured")
	}
	all := strings.EqualFold(strings.TrimSpace(args), "all")
	workspace := workspaceSnapshotRoot(rt.workspace)
	policy, err := LoadPersistentMemoryPolicy(workspace)
	if err != nil {
		return err
	}
	result, err := rt.longMem.Prune(workspace, policy, all)
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Memory Prune"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("scope", result.Scope))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("before", fmt.Sprintf("%d", result.Before)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("after", fmt.Sprintf("%d", result.After)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("deleted", fmt.Sprintf("%d", result.Deleted)))
	for i := range result.DeletedIDs {
		fmt.Fprintf(rt.writer, "%s  %s\n", rt.ui.dim(result.DeletedIDs[i]), result.DeletedReason[i])
	}
	fmt.Fprintln(rt.writer)
	fmt.Fprintln(rt.writer, renderCommandHandoff("Memory", commandHandoffPlan{
		Title: "Memory was pruned; inspect the remaining reusable context when needed.",
		Commands: []commandHandoffCommand{
			{Label: "Inspect", Command: "/mem-dashboard"},
		},
	}))
	return nil
}

func (rt *runtimeState) handlePersistentMemoryStats() error {
	if rt.longMem == nil {
		return fmt.Errorf("persistent memory is not configured")
	}
	stats, err := rt.longMem.Stats()
	if err != nil {
		return err
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Memory Stats"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("path", stats.Path))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("records", fmt.Sprintf("%d", stats.Count)))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("workspaces", fmt.Sprintf("%d", stats.WorkspaceSet)))
	if !stats.LastUpdated.IsZero() {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("last_updated", stats.LastUpdated.Format(time.RFC3339)))
	}
	return nil
}
