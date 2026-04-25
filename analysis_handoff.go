package main

import (
	"fmt"
	"strings"
)

type analysisProjectHandoffPlan struct {
	Title    string
	Details  []string
	Commands []analysisProjectHandoffCommand
}

type analysisProjectHandoffCommand struct {
	Label   string
	Command string
}

func buildAnalysisProjectHandoff(run ProjectAnalysisRun, manifest AnalysisDocsManifest, manifestOK bool) analysisProjectHandoffPlan {
	plan := analysisProjectHandoffPlan{
		Title: "Open the dashboard first, then let Kernforge carry the reusable docs into fuzzing or verification.",
		Commands: []analysisProjectHandoffCommand{
			{Label: "Continue", Command: "/analyze-dashboard"},
		},
	}
	if runID := strings.TrimSpace(run.Summary.RunID); runID != "" {
		plan.Details = append(plan.Details, "Run: "+runID)
	}
	if !manifestOK {
		plan.Title = "The analysis run is ready, but the docs manifest could not be loaded for guided reuse."
		plan.Details = append(plan.Details, "Refresh docs before using generated targets in other planners.")
		plan.Commands = append(plan.Commands, analysisProjectHandoffCommand{Label: "Repair", Command: "/docs-refresh"})
		return normalizeAnalysisProjectHandoffPlan(plan)
	}

	if manifest.DocumentCount > 0 {
		plan.Details = append(plan.Details, fmt.Sprintf("Docs: %d generated document(s) with source anchors and stale markers.", manifest.DocumentCount))
	}
	if len(manifest.FuzzTargets) > 0 {
		plan.Details = append(plan.Details, fmt.Sprintf("Fuzzing: %d target candidate(s) are available from FUZZ_TARGETS.md.", len(manifest.FuzzTargets)))
		plan.Commands = append(plan.Commands, analysisProjectHandoffCommand{Label: "Fuzz next", Command: "/fuzz-campaign run"})
		if command := firstAnalysisFuzzTargetSuggestedCommand(manifest.FuzzTargets); command != "" {
			plan.Commands = append(plan.Commands, analysisProjectHandoffCommand{Label: "Target drilldown", Command: command})
		}
	}
	if len(manifest.VerificationMatrix) > 0 {
		plan.Details = append(plan.Details, fmt.Sprintf("Verification: %d matrix item(s) can guide /verify planning.", len(manifest.VerificationMatrix)))
		plan.Commands = append(plan.Commands, analysisProjectHandoffCommand{Label: "Verify next", Command: "/verify"})
	}
	return normalizeAnalysisProjectHandoffPlan(plan)
}

func firstAnalysisFuzzTargetSuggestedCommand(targets []AnalysisFuzzTargetCatalogEntry) string {
	for _, target := range targets {
		command := strings.TrimSpace(target.SuggestedCommand)
		if command != "" {
			return command
		}
	}
	for _, target := range targets {
		name := strings.TrimSpace(target.Name)
		if name != "" {
			return "/fuzz-func " + name
		}
	}
	return ""
}

func normalizeAnalysisProjectHandoffPlan(plan analysisProjectHandoffPlan) analysisProjectHandoffPlan {
	title := strings.TrimSpace(plan.Title)
	if title == "" {
		return analysisProjectHandoffPlan{}
	}
	plan.Title = title
	details := make([]string, 0, len(plan.Details))
	for _, detail := range plan.Details {
		detail = strings.TrimSpace(detail)
		if detail != "" {
			details = append(details, detail)
		}
	}
	plan.Details = details
	commands := make([]analysisProjectHandoffCommand, 0, len(plan.Commands))
	seen := map[string]struct{}{}
	for _, command := range plan.Commands {
		label := strings.TrimSpace(command.Label)
		value := strings.TrimSpace(command.Command)
		if label == "" || value == "" {
			continue
		}
		key := strings.ToLower(label + "\x00" + value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		commands = append(commands, analysisProjectHandoffCommand{Label: label, Command: value})
	}
	plan.Commands = commands
	return plan
}

func renderAnalysisProjectHandoff(plan analysisProjectHandoffPlan) string {
	plan = normalizeAnalysisProjectHandoffPlan(plan)
	if strings.TrimSpace(plan.Title) == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Analysis handoff: %s\n", plan.Title)
	for _, detail := range plan.Details {
		fmt.Fprintf(&b, "- %s\n", detail)
	}
	for _, command := range plan.Commands {
		fmt.Fprintf(&b, "%s: %s\n", command.Label, command.Command)
	}
	return strings.TrimRight(b.String(), "\n")
}
