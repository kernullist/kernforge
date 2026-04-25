package main

import (
	"fmt"
	"sort"
	"strings"
)

func (rt *runtimeState) handleEvidenceRecent(args string) error {
	if rt.evidence == nil {
		return fmt.Errorf("evidence store is not configured")
	}
	if strings.TrimSpace(args) != "" {
		return rt.handleEvidenceSearch(args)
	}
	records, err := rt.evidence.ListRecent(rt.workspace.BaseRoot, 8)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No evidence records found."))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Evidence"))
	for _, record := range records {
		line := fmt.Sprintf("- %s  kind=%s", rt.ui.dim(record.ID), record.Kind)
		if strings.TrimSpace(record.Category) != "" {
			line += "  category=" + record.Category
		}
		if strings.TrimSpace(record.Outcome) != "" {
			line += "  outcome=" + record.Outcome
		}
		if strings.TrimSpace(record.Severity) != "" {
			line += "  severity=" + record.Severity
		}
		if strings.TrimSpace(record.SignalClass) != "" {
			line += "  signal=" + record.SignalClass
		}
		if record.RiskScore > 0 {
			line += fmt.Sprintf("  risk=%d", record.RiskScore)
		}
		line += "  " + record.Subject
		fmt.Fprintln(rt.writer, line)
	}
	if handoff := evidenceHandoff(records); strings.TrimSpace(handoff) != "" {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, handoff)
	}
	return nil
}

func (rt *runtimeState) handleEvidenceSearch(query string) error {
	if rt.evidence == nil {
		return fmt.Errorf("evidence store is not configured")
	}
	if strings.TrimSpace(query) == "" {
		return fmt.Errorf("usage: /evidence-search <query>")
	}
	records, err := rt.evidence.Search(query, rt.workspace.BaseRoot, 12)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Fprintln(rt.writer, rt.ui.warnLine("No matching evidence records found."))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Evidence Search"))
	for _, record := range records {
		line := fmt.Sprintf("- %s  kind=%s", rt.ui.dim(record.ID), record.Kind)
		if strings.TrimSpace(record.Category) != "" {
			line += "  category=" + record.Category
		}
		if strings.TrimSpace(record.Outcome) != "" {
			line += "  outcome=" + record.Outcome
		}
		if strings.TrimSpace(record.Severity) != "" {
			line += "  severity=" + record.Severity
		}
		if strings.TrimSpace(record.SignalClass) != "" {
			line += "  signal=" + record.SignalClass
		}
		if record.RiskScore > 0 {
			line += fmt.Sprintf("  risk=%d", record.RiskScore)
		}
		line += "  " + record.Subject
		if strings.TrimSpace(record.VerificationSummary) != "" {
			line += "  |  " + compactPersistentMemoryText(record.VerificationSummary, 120)
		}
		fmt.Fprintln(rt.writer, line)
	}
	if handoff := evidenceHandoff(records); strings.TrimSpace(handoff) != "" {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, handoff)
	}
	return nil
}

func (rt *runtimeState) handleEvidenceShow(id string) error {
	if rt.evidence == nil {
		return fmt.Errorf("evidence store is not configured")
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("usage: /evidence-show <id>")
	}
	record, ok, err := rt.evidence.Get(id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("evidence record not found: %s", id)
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Evidence Record"))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("id", record.ID))
	fmt.Fprintln(rt.writer, rt.ui.statusKV("kind", record.Kind))
	if strings.TrimSpace(record.Category) != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("category", record.Category))
	}
	fmt.Fprintln(rt.writer, rt.ui.statusKV("subject", record.Subject))
	if strings.TrimSpace(record.Outcome) != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("outcome", record.Outcome))
	}
	if strings.TrimSpace(record.Severity) != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("severity", record.Severity))
	}
	if strings.TrimSpace(record.Confidence) != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("confidence", record.Confidence))
	}
	if strings.TrimSpace(record.SignalClass) != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("signal_class", record.SignalClass))
	}
	if record.RiskScore > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("risk_score", fmt.Sprintf("%d", record.RiskScore)))
	}
	if strings.TrimSpace(record.VerificationSummary) != "" {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("verification", record.VerificationSummary))
	}
	if len(record.SeverityReasons) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("severity_reasons", strings.Join(record.SeverityReasons, ", ")))
	}
	if len(record.Tags) > 0 {
		fmt.Fprintln(rt.writer, rt.ui.statusKV("tags", strings.Join(record.Tags, ", ")))
	}
	if len(record.Attributes) > 0 {
		var attrs []string
		for key, value := range record.Attributes {
			attrs = append(attrs, key+"="+value)
		}
		sort.Strings(attrs)
		fmt.Fprintln(rt.writer, rt.ui.statusKV("attributes", strings.Join(attrs, ", ")))
	}
	if handoff := evidenceHandoff([]EvidenceRecord{record}); strings.TrimSpace(handoff) != "" {
		fmt.Fprintln(rt.writer)
		fmt.Fprintln(rt.writer, handoff)
	}
	return nil
}

func (rt *runtimeState) handleEvidenceDashboard(query string, html bool) error {
	if rt.evidence == nil {
		return fmt.Errorf("evidence store is not configured")
	}
	summary, err := rt.evidence.Dashboard(rt.workspace.BaseRoot, query, 12)
	if err != nil {
		return err
	}
	if rt.hookOverrides != nil {
		if items, err := rt.hookOverrides.List(rt.workspace.BaseRoot); err == nil {
			summary.ActiveOverrides = items
		}
	}
	if html {
		outputPath, err := createEvidenceDashboardHTML(summary)
		if err != nil {
			return err
		}
		if err := OpenExternalURL(outputPath); err != nil {
			fmt.Fprintln(rt.writer, rt.ui.warnLine("Generated HTML evidence dashboard but could not open it automatically: "+err.Error()))
		}
		fmt.Fprintln(rt.writer, rt.ui.successLine("Generated evidence dashboard: "+outputPath))
		return nil
	}
	fmt.Fprintln(rt.writer, rt.ui.section("Evidence Dashboard"))
	fmt.Fprintln(rt.writer, renderEvidenceDashboard(summary))
	return nil
}
