package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (rt *runtimeState) handleInvestigateCommand(args string) error {
	if rt.investigations == nil {
		return fmt.Errorf("investigation store is not configured")
	}
	trimmed := strings.TrimSpace(args)
	if trimmed == "" || strings.EqualFold(trimmed, "status") {
		return rt.showInvestigationStatus()
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return rt.showInvestigationStatus()
	}
	subcmd := strings.ToLower(fields[0])
	rest := ""
	if len(fields) > 1 {
		rest = strings.TrimSpace(trimmed[len(fields[0]):])
	}
	switch subcmd {
	case "start":
		return rt.handleInvestigationStart(rest)
	case "snapshot":
		return rt.handleInvestigationSnapshot(rest)
	case "note":
		return rt.handleInvestigationNote(rest)
	case "stop":
		return rt.handleInvestigationStop(rest)
	case "show":
		return rt.handleInvestigationShow(rest)
	case "list":
		return rt.handleInvestigationList()
	case "dashboard":
		return rt.handleInvestigationDashboard(false)
	case "dashboard-html":
		return rt.handleInvestigationDashboard(true)
	default:
		return fmt.Errorf("usage: /investigate [start|snapshot|note|stop|show|list|dashboard|dashboard-html]")
	}
}

func (rt *runtimeState) handleInvestigationDashboard(html bool) error {
	summary, err := rt.investigations.Dashboard(rt.workspace.BaseRoot, 8)
	if err != nil {
		return err
	}
	if html {
		outputPath, err := createInvestigationDashboardHTML(summary)
		if err != nil {
			return err
		}
		if err := OpenExternalURL(outputPath); err != nil {
			fmt.Fprintln(rt.writer, rt.ui.warnLine("Generated HTML investigation dashboard but could not open it automatically: "+err.Error()))
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Generated investigation dashboard: "+outputPath))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Investigation Dashboard"))
	fmt.Fprintln(rt.writer, renderInvestigationDashboard(summary))
	return nil
}

func (rt *runtimeState) showInvestigationStatus() error {
	fmt.Fprintln(rt.writer, rt.ui.section("Investigation"))
	if active, ok, err := rt.investigations.Active(rt.workspace.BaseRoot); err == nil && ok {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("active", "true"))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("id", active.ID))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("preset", active.Preset))
		if strings.TrimSpace(active.Target) != "" {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("target", active.Target))
		}
		fmt.Fprintln(rt.writer, rt.ui.statusKV("snapshots", fmt.Sprintf("%d", len(active.Snapshots))))
		if len(active.Snapshots) > 0 {
			latest := active.Snapshots[len(active.Snapshots)-1]
			fmt.Fprintln(rt.writer, rt.ui.statusKV("latest_snapshot", latest.ID))
			for _, finding := range latest.Findings {
				fmt.Fprintln(rt.writer, rt.ui.dim(fmt.Sprintf("%s  severity=%s  risk=%d", finding.Subject, finding.Severity, finding.RiskScore)))
			}
		}
	} else {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("active", "false"))
	}
	count, _, last, err := rt.investigations.Stats(rt.workspace.BaseRoot)
	if err == nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("sessions", fmt.Sprintf("%d", count)))
		if !last.IsZero() {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("last_updated", last.Format(time.RFC3339)))
		}
	}
	return nil
}

func (rt *runtimeState) handleInvestigationStart(args string) error {
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) < 1 {
		return fmt.Errorf("usage: /investigate start <preset> [target]")
	}
	if active, ok, err := rt.investigations.Active(rt.workspace.BaseRoot); err == nil && ok {
		return fmt.Errorf("an active investigation already exists: %s", active.ID)
	}
	preset := normalizeInvestigationPreset(fields[0])
	target := ""
	if len(fields) > 1 {
		target = strings.Join(fields[1:], " ")
	}
	record, err := rt.investigations.Append(InvestigationRecord{
		Workspace:          workspaceSnapshotRoot(rt.workspace),
		Preset:             preset,
		Target:             target,
		Status:             InvestigationActive,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
		StartedBySessionID: rt.session.ID,
	})
	if err != nil {
		return err
	}
	rt.appendInvestigationSessionEvidence(record, "active")
	fmt.Fprintln(rt.writer, rt.ui.successLine("Started investigation "+record.ID))
	if handoff := investigationHandoffAfterStart(record); strings.TrimSpace(handoff) != "" {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, handoff)
	}
	return nil
}

func (rt *runtimeState) handleInvestigationSnapshot(args string) error {
	active, ok, err := rt.investigations.Active(rt.workspace.BaseRoot)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no active investigation. Start one with /investigate start <preset> [target]")
	}
	target := strings.TrimSpace(args)
	if target == "" {
		target = active.Target
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	snapshot := collectInvestigationSnapshot(ctx, rt.workspace, active.Preset, target, rt.evidence)
	updated, ok, err := rt.investigations.Update(active.ID, func(record *InvestigationRecord) error {
		record.UpdatedAt = time.Now()
		record.Snapshots = append(record.Snapshots, snapshot)
		record.Summary = snapshot.RawSummary
		return nil
	})
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("investigation not found: %s", active.ID)
	}
	rt.appendInvestigationSnapshotEvidence(updated, snapshot)
	fmt.Fprintln(rt.writer, rt.ui.successLine(fmt.Sprintf("Captured snapshot %s with %d finding(s)", snapshot.ID, len(snapshot.Findings))))
	if handoff := investigationHandoffAfterSnapshot(updated, snapshot); strings.TrimSpace(handoff) != "" {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, handoff)
	}
	return nil
}

func (rt *runtimeState) handleInvestigationNote(args string) error {
	text := strings.TrimSpace(args)
	if text == "" {
		return fmt.Errorf("usage: /investigate note <text>")
	}
	active, ok, err := rt.investigations.Active(rt.workspace.BaseRoot)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no active investigation")
	}
	_, ok, err = rt.investigations.Update(active.ID, func(record *InvestigationRecord) error {
		record.UpdatedAt = time.Now()
		record.Notes = append(record.Notes, text)
		return nil
	})
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("investigation not found: %s", active.ID)
	}
	fmt.Fprintln(rt.writer, rt.ui.successLine("Added investigation note"))
	return nil
}

func (rt *runtimeState) handleInvestigationStop(args string) error {
	active, ok, err := rt.investigations.Active(rt.workspace.BaseRoot)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no active investigation")
	}
	summary := strings.TrimSpace(args)
	updated, ok, err := rt.investigations.Update(active.ID, func(record *InvestigationRecord) error {
		record.Status = InvestigationCompleted
		record.UpdatedAt = time.Now()
		if summary != "" {
			record.Summary = summary
		} else if record.Summary == "" && len(record.Snapshots) > 0 {
			record.Summary = record.Snapshots[len(record.Snapshots)-1].RawSummary
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("investigation not found: %s", active.ID)
	}
	rt.appendInvestigationSessionEvidence(updated, "completed")
	rt.appendInvestigationMemory(updated)
	fmt.Fprintln(rt.writer, rt.ui.successLine("Completed investigation "+updated.ID))
	if handoff := investigationHandoffAfterStop(updated); strings.TrimSpace(handoff) != "" {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, handoff)
	}
	return nil
}

func (rt *runtimeState) handleInvestigationShow(args string) error {
	id := strings.TrimSpace(args)
	if id == "" {
		return fmt.Errorf("usage: /investigate show <id>")
	}
	record, ok, err := rt.investigations.Get(id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("investigation not found: %s", id)
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Investigation Record"))
	fmt.Fprintln(rt.writer, renderInvestigationRecord(rt.workspace.Root, record))
	return nil
}

func (rt *runtimeState) handleInvestigationList() error {
	items, err := rt.investigations.ListRecent(rt.workspace.BaseRoot, 8)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No investigations found for this workspace."))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Investigations"))
	for _, item := range items {
		line := fmt.Sprintf("- %s  preset=%s  status=%s", rt.ui.dim(item.ID), item.Preset, item.Status)
		if strings.TrimSpace(item.Target) != "" {
			line += "  target=" + item.Target
		}
		if strings.TrimSpace(item.Summary) != "" {
			line += "  |  " + compactPersistentMemoryText(item.Summary, 120)
		}
		fmt.Fprintln(rt.writer, line)
	}
	return nil
}

func (rt *runtimeState) appendInvestigationSessionEvidence(record InvestigationRecord, outcome string) {
	if rt.evidence == nil {
		return
	}
	_ = rt.evidence.Append(EvidenceRecord{
		SessionID:           rt.session.ID,
		Workspace:           workspaceSnapshotRoot(rt.workspace),
		CreatedAt:           time.Now(),
		Kind:                "investigation_session",
		Category:            investigationCategoryForPreset(record.Preset),
		Subject:             record.Preset + ":" + valueOrUnset(record.Target),
		Outcome:             outcome,
		Severity:            "low",
		Confidence:          "high",
		SignalClass:         "investigation",
		RiskScore:           15,
		Tags:                []string{"investigation", record.Preset},
		VerificationSummary: compactPersistentMemoryText(record.Summary, 180),
	})
}

func (rt *runtimeState) appendInvestigationSnapshotEvidence(record InvestigationRecord, snapshot InvestigationSnapshot) {
	if rt.evidence == nil {
		return
	}
	_ = rt.evidence.Append(EvidenceRecord{
		SessionID:           rt.session.ID,
		Workspace:           workspaceSnapshotRoot(rt.workspace),
		CreatedAt:           time.Now(),
		Kind:                "investigation_snapshot",
		Category:            investigationCategoryForPreset(record.Preset),
		Subject:             snapshot.Kind + ":" + snapshot.ID,
		Outcome:             "captured",
		Severity:            "low",
		Confidence:          "high",
		SignalClass:         "investigation",
		RiskScore:           20,
		Tags:                []string{"investigation", "snapshot", record.Preset},
		VerificationSummary: snapshot.RawSummary,
	})
	for _, finding := range snapshot.Findings {
		_ = rt.evidence.Append(EvidenceRecord{
			SessionID:           rt.session.ID,
			Workspace:           workspaceSnapshotRoot(rt.workspace),
			CreatedAt:           time.Now(),
			Kind:                "investigation_finding",
			Category:            finding.Category,
			Subject:             finding.Subject,
			Outcome:             finding.Outcome,
			Severity:            finding.Severity,
			Confidence:          "medium",
			SignalClass:         finding.SignalClass,
			RiskScore:           finding.RiskScore,
			Tags:                []string{"investigation", record.Preset, snapshot.Kind},
			Attributes:          finding.Attributes,
			VerificationSummary: finding.Message,
		})
	}
}

func (rt *runtimeState) appendInvestigationMemory(record InvestigationRecord) {
	if rt.longMem == nil {
		return
	}
	var topFindings []string
	if len(record.Snapshots) > 0 {
		latest := record.Snapshots[len(record.Snapshots)-1]
		for _, finding := range latest.Findings {
			topFindings = append(topFindings, finding.Subject)
			if len(topFindings) >= 4 {
				break
			}
		}
	}
	summary := fmt.Sprintf("Investigation %s for preset %s target %s completed. Findings: %s", record.ID, record.Preset, valueOrUnset(record.Target), strings.Join(topFindings, ", "))
	_ = rt.longMem.Append(PersistentMemoryRecord{
		SessionID:   rt.session.ID,
		SessionName: rt.session.Name,
		Provider:    rt.session.Provider,
		Model:       rt.session.Model,
		Workspace:   workspaceSnapshotRoot(rt.workspace),
		CreatedAt:   time.Now(),
		Request:     "investigation stop",
		Reply:       summary,
		Summary:     compactPersistentMemoryText(summary, 240),
		Importance:  PersistentMemoryMedium,
		Trust:       PersistentMemoryConfirmed,
		Keywords:    uniqueStrings(append([]string{"investigation", record.Preset}, topFindings...)),
	})
}
