package main

import (
	"fmt"
	"strings"
	"time"
)

func (rt *runtimeState) handleSimulateCommand(args string) error {
	if rt.simulations == nil {
		return fmt.Errorf("simulation store is not configured")
	}
	trimmed := strings.TrimSpace(args)
	if trimmed == "" || strings.EqualFold(trimmed, "status") {
		return rt.showSimulationStatus()
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return rt.showSimulationStatus()
	}
	switch strings.ToLower(fields[0]) {
	case "show":
		return rt.handleSimulationShow(strings.TrimSpace(trimmed[len(fields[0]):]))
	case "list":
		return rt.handleSimulationList()
	case "dashboard":
		return rt.handleSimulationDashboard(false)
	case "dashboard-html":
		return rt.handleSimulationDashboard(true)
	default:
		return rt.handleSimulationRun(trimmed)
	}
}

func (rt *runtimeState) handleSimulationDashboard(html bool) error {
	summary, err := rt.simulations.Dashboard(rt.workspace.BaseRoot, 8)
	if err != nil {
		return err
	}
	if html {
		outputPath, err := createSimulationDashboardHTML(summary)
		if err != nil {
			return err
		}
		if err := OpenExternalURL(outputPath); err != nil {
			fmt.Fprintln(rt.writer, rt.ui.warnLine("Generated HTML simulation dashboard but could not open it automatically: "+err.Error()))
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Generated simulation dashboard: "+outputPath))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Simulation Dashboard"))
	fmt.Fprintln(rt.writer, renderSimulationDashboard(summary))
	return nil
}

func (rt *runtimeState) showSimulationStatus() error {
	fmt.Fprintln(rt.writer, rt.ui.section("Simulation"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("profiles", "tamper-surface, stealth-surface, forensic-blind-spot"))
	if items, err := rt.simulations.ListRecent(rt.workspace.BaseRoot, 1); err == nil && len(items) > 0 {
		latest := items[0]
		fmt.Fprintln(rt.writer, rt.ui.statusKV("latest", latest.ID))
		fmt.Fprintln(rt.writer, rt.ui.statusKV("profile", latest.Profile))
		if strings.TrimSpace(latest.Target) != "" {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("target", latest.Target))
		}
		if len(latest.Findings) > 0 {
			top := latest.Findings[0]
			fmt.Fprintln(rt.writer, rt.ui.statusKV("top_finding", fmt.Sprintf("%s (risk=%d)", top.Subject, top.RiskScore)))
		}
	}
	count, last, err := rt.simulations.Stats(rt.workspace.BaseRoot)
	if err == nil {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("results", fmt.Sprintf("%d", count)))
		if !last.IsZero() {
			fmt.Fprintln(rt.writer, rt.ui.statusKV("last_updated", last.Format(time.RFC3339)))
		}
	}
	return nil
}

func (rt *runtimeState) handleSimulationRun(args string) error {
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) < 1 {
		return fmt.Errorf("usage: /simulate <profile> [target]")
	}
	profile := strings.TrimSpace(fields[0])
	target := ""
	if len(fields) > 1 {
		target = strings.Join(fields[1:], " ")
	}
	evidenceItems := []EvidenceRecord{}
	if rt.evidence != nil {
		if items, err := rt.evidence.Search("outcome:failed", rt.workspace.BaseRoot, 12); err == nil {
			evidenceItems = items
		}
	}
	investigationItems := []InvestigationRecord{}
	if rt.investigations != nil {
		if items, err := rt.investigations.ListRecent(rt.workspace.BaseRoot, 3); err == nil {
			investigationItems = items
		}
	}
	result := runSimulationProfile(profile, target, workspaceSnapshotRoot(rt.workspace), evidenceItems, investigationItems, rt.session.LastVerification)
	saved, err := rt.simulations.Append(result)
	if err != nil {
		return err
	}
	rt.appendSimulationEvidence(saved)
	rt.appendSimulationMemory(saved)
	fmt.Fprintln(rt.writer, rt.ui.section("Simulation"))
	fmt.Fprintln(rt.writer, renderSimulationResult(saved))
	if handoff := simulationHandoff(saved); strings.TrimSpace(handoff) != "" {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, handoff)
	}
	return nil
}

func (rt *runtimeState) handleSimulationList() error {
	items, err := rt.simulations.ListRecent(rt.workspace.BaseRoot, 8)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No simulations found for this workspace."))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Simulations"))
	for _, item := range items {
		line := fmt.Sprintf("- %s  profile=%s", rt.ui.dim(item.ID), item.Profile)
		if strings.TrimSpace(item.Target) != "" {
			line += "  target=" + item.Target
		}
		line += fmt.Sprintf("  findings=%d", len(item.Findings))
		if strings.TrimSpace(item.Summary) != "" {
			line += "  |  " + compactPersistentMemoryText(item.Summary, 120)
		}
		fmt.Fprintln(rt.writer, line)
	}
	return nil
}

func (rt *runtimeState) handleSimulationShow(args string) error {
	id := strings.TrimSpace(args)
	if id == "" {
		return fmt.Errorf("usage: /simulate show <id>")
	}
	result, ok, err := rt.simulations.Get(id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("simulation not found: %s", id)
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Simulation Result"))
	fmt.Fprintln(rt.writer, renderSimulationResult(result))
	if handoff := simulationHandoff(result); strings.TrimSpace(handoff) != "" {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, handoff)
	}
	return nil
}

func (rt *runtimeState) appendSimulationEvidence(result SimulationResult) {
	if rt.evidence == nil {
		return
	}
	_ = rt.evidence.Append(EvidenceRecord{
		SessionID:           rt.session.ID,
		Workspace:           workspaceSnapshotRoot(rt.workspace),
		CreatedAt:           time.Now(),
		Kind:                "simulation_run",
		Category:            inferSimulationCategory(result),
		Subject:             result.Profile + ":" + valueOrUnset(result.Target),
		Outcome:             "completed",
		Severity:            "low",
		Confidence:          "high",
		SignalClass:         "simulation",
		RiskScore:           18,
		Tags:                []string{"simulation", result.Profile},
		VerificationSummary: result.Summary,
	})
	for _, finding := range result.Findings {
		_ = rt.evidence.Append(EvidenceRecord{
			SessionID:           rt.session.ID,
			Workspace:           workspaceSnapshotRoot(rt.workspace),
			CreatedAt:           time.Now(),
			Kind:                "simulation_finding",
			Category:            finding.Category,
			Subject:             finding.Subject,
			Outcome:             "failed",
			Severity:            finding.Severity,
			Confidence:          "medium",
			SignalClass:         finding.SignalClass,
			RiskScore:           finding.RiskScore,
			Tags:                append([]string{"simulation", result.Profile}, finding.Kind),
			Attributes:          finding.Attributes,
			VerificationSummary: finding.Message,
		})
	}
}

func (rt *runtimeState) appendSimulationMemory(result SimulationResult) {
	if rt.longMem == nil {
		return
	}
	var top []string
	for _, finding := range result.Findings {
		top = append(top, finding.Subject)
		if len(top) >= 4 {
			break
		}
	}
	summary := fmt.Sprintf("Simulation %s profile %s target %s. Findings: %s", result.ID, result.Profile, valueOrUnset(result.Target), strings.Join(top, ", "))
	_ = rt.longMem.Append(PersistentMemoryRecord{
		SessionID:   rt.session.ID,
		SessionName: rt.session.Name,
		Provider:    rt.session.Provider,
		Model:       rt.session.Model,
		Workspace:   workspaceSnapshotRoot(rt.workspace),
		CreatedAt:   time.Now(),
		Request:     "simulation run",
		Reply:       summary,
		Summary:     compactPersistentMemoryText(summary, 240),
		Importance:  PersistentMemoryMedium,
		Trust:       PersistentMemoryConfirmed,
		Keywords:    uniqueStrings(append([]string{"simulation", result.Profile}, top...)),
	})
}

func inferSimulationCategory(result SimulationResult) string {
	for _, finding := range result.Findings {
		if strings.TrimSpace(finding.Category) != "" {
			return finding.Category
		}
	}
	return "policy"
}
