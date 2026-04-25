package main

import (
	"fmt"
	"strings"
)

type commandHandoffPlan struct {
	Title    string
	Details  []string
	Commands []commandHandoffCommand
}

type commandHandoffCommand struct {
	Label   string
	Command string
}

func renderCommandHandoff(name string, plan commandHandoffPlan) string {
	name = strings.TrimSpace(name)
	plan = normalizeCommandHandoffPlan(plan)
	if name == "" || strings.TrimSpace(plan.Title) == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s handoff: %s\n", name, plan.Title)
	for _, detail := range plan.Details {
		fmt.Fprintf(&b, "- %s\n", detail)
	}
	for _, command := range plan.Commands {
		fmt.Fprintf(&b, "%s: %s\n", command.Label, command.Command)
	}
	return strings.TrimRight(b.String(), "\n")
}

func normalizeCommandHandoffPlan(plan commandHandoffPlan) commandHandoffPlan {
	plan.Title = strings.TrimSpace(plan.Title)
	details := make([]string, 0, len(plan.Details))
	for _, detail := range plan.Details {
		detail = strings.TrimSpace(detail)
		if detail != "" {
			details = append(details, detail)
		}
	}
	plan.Details = details
	commands := make([]commandHandoffCommand, 0, len(plan.Commands))
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
		commands = append(commands, commandHandoffCommand{Label: label, Command: value})
	}
	plan.Commands = commands
	return plan
}

func investigationHandoffAfterStart(record InvestigationRecord) string {
	return renderCommandHandoff("Investigation", commandHandoffPlan{
		Title: "Capture the first snapshot so this session becomes actionable evidence.",
		Details: []string{
			"Preset: " + record.Preset,
			"Target: " + valueOrUnset(record.Target),
		},
		Commands: []commandHandoffCommand{
			{Label: "Continue", Command: "/investigate snapshot"},
			{Label: "Explore", Command: "/investigate dashboard"},
		},
	})
}

func investigationHandoffAfterSnapshot(record InvestigationRecord, snapshot InvestigationSnapshot) string {
	title := "Review the captured findings, then run a risk simulation or evidence dashboard."
	commands := []commandHandoffCommand{
		{Label: "Explore", Command: "/investigate dashboard"},
		{Label: "Evidence", Command: "/evidence-dashboard"},
	}
	if len(snapshot.Findings) > 0 {
		commands = append([]commandHandoffCommand{{Label: "Next", Command: "/simulate " + simulationProfileForInvestigation(record.Preset)}}, commands...)
	}
	return renderCommandHandoff("Investigation", commandHandoffPlan{
		Title: title,
		Details: []string{
			fmt.Sprintf("Snapshot: %s findings=%d", snapshot.ID, len(snapshot.Findings)),
		},
		Commands: commands,
	})
}

func investigationHandoffAfterStop(record InvestigationRecord) string {
	commands := []commandHandoffCommand{
		{Label: "Evidence", Command: "/evidence-dashboard"},
	}
	if latest := latestInvestigationSnapshot(record); latest != nil && len(latest.Findings) > 0 {
		commands = append([]commandHandoffCommand{
			{Label: "Next", Command: "/simulate " + simulationProfileForInvestigation(record.Preset)},
			{Label: "Verify", Command: "/verify"},
		}, commands...)
	}
	return renderCommandHandoff("Investigation", commandHandoffPlan{
		Title: "The session is recorded; carry confirmed observations into simulation, verification, or evidence review.",
		Details: []string{
			"Investigation: " + record.ID,
		},
		Commands: commands,
	})
}

func latestInvestigationSnapshot(record InvestigationRecord) *InvestigationSnapshot {
	if len(record.Snapshots) == 0 {
		return nil
	}
	return &record.Snapshots[len(record.Snapshots)-1]
}

func simulationProfileForInvestigation(preset string) string {
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "stealth", "visibility", "forensic", "forensic-blind-spot":
		return "forensic-blind-spot"
	case "tamper", "tamper-surface", "integrity":
		return "tamper-surface"
	default:
		return "stealth-surface"
	}
}

func simulationHandoff(result SimulationResult) string {
	commands := []commandHandoffCommand{
		{Label: "Explore", Command: "/simulate-dashboard"},
		{Label: "Evidence", Command: "/evidence-dashboard"},
	}
	if len(result.Findings) > 0 {
		commands = append([]commandHandoffCommand{{Label: "Verify", Command: "/verify"}}, commands...)
	}
	return renderCommandHandoff("Simulation", commandHandoffPlan{
		Title: "Treat the risk lens as evidence and either verify it or inspect the dashboard.",
		Details: []string{
			fmt.Sprintf("Run: %s findings=%d max_risk=%d", result.ID, len(result.Findings), maxSimulationRisk(result)),
		},
		Commands: commands,
	})
}

func maxSimulationRisk(result SimulationResult) int {
	maxRisk := 0
	for _, finding := range result.Findings {
		if finding.RiskScore > maxRisk {
			maxRisk = finding.RiskScore
		}
	}
	return maxRisk
}

func verificationHandoff(report VerificationReport, activeFeature FeatureWorkflow, hasActiveFeature bool) string {
	if report.HasFailures() {
		return renderCommandHandoff("Verification", commandHandoffPlan{
			Title: "Repair the first failing step, then rerun the same verification path.",
			Details: []string{
				"First failure: " + verificationFailureLabel(report),
			},
			Commands: []commandHandoffCommand{
				{Label: "Retry", Command: "/verify"},
				{Label: "Inspect", Command: "/verify-dashboard"},
				{Label: "Evidence", Command: "/evidence-dashboard"},
			},
		})
	}
	commands := []commandHandoffCommand{
		{Label: "Inspect", Command: "/verify-dashboard"},
		{Label: "Checkpoint", Command: "/checkpoint verified-state"},
	}
	if hasActiveFeature && strings.EqualFold(activeFeature.Status, featureStatusImplemented) {
		commands = append([]commandHandoffCommand{{Label: "Close feature", Command: "/new-feature close"}}, commands...)
	} else if hasActiveFeature {
		commands = append([]commandHandoffCommand{{Label: "Feature", Command: "/new-feature status"}}, commands...)
	}
	return renderCommandHandoff("Verification", commandHandoffPlan{
		Title: "Verification passed; preserve the known-good state or continue the active feature workflow.",
		Details: []string{
			report.SummaryLine(),
		},
		Commands: commands,
	})
}

func verificationFailureLabel(report VerificationReport) string {
	if failure := report.FirstFailure(); failure != nil {
		if strings.TrimSpace(failure.FailureKind) != "" {
			return failure.Label + " [" + failure.FailureKind + "]"
		}
		return failure.Label
	}
	return "unknown"
}

func performanceHandoff(result PerformanceAnalysisResult) string {
	commands := []commandHandoffCommand{
		{Label: "Open context", Command: "/analyze-dashboard"},
		{Label: "Verify", Command: "/verify"},
		{Label: "Risk lens", Command: "/simulate stealth-surface"},
	}
	if command := fuzzCommandForPerformanceResult(result); command != "" {
		commands = append([]commandHandoffCommand{{Label: "Target drilldown", Command: command}}, commands...)
	}
	return renderCommandHandoff("Performance", commandHandoffPlan{
		Title: "Use the hotspot lens to pick one concrete verification or fuzzing follow-up.",
		Details: []string{
			fmt.Sprintf("Hotspots: %d", len(result.TopHotspots)),
		},
		Commands: commands,
	})
}

func fuzzCommandForPerformanceResult(result PerformanceAnalysisResult) string {
	for _, hotspot := range result.TopHotspots {
		for _, entry := range hotspot.EntryPoints {
			entry = strings.TrimSpace(entry)
			if entry != "" {
				return "/fuzz-func " + entry
			}
		}
	}
	return ""
}

func evidenceHandoff(records []EvidenceRecord) string {
	top, ok := mostActionableEvidence(records)
	if !ok {
		return ""
	}
	commands := []commandHandoffCommand{
		{Label: "Explore", Command: "/evidence-dashboard"},
	}
	if evidenceNeedsVerification(top) {
		commands = append([]commandHandoffCommand{{Label: "Verify", Command: "/verify"}}, commands...)
	}
	if command := evidenceSourceDashboardCommand(top); command != "" {
		commands = append(commands, commandHandoffCommand{Label: "Source", Command: command})
	}
	return renderCommandHandoff("Evidence", commandHandoffPlan{
		Title: "Use the strongest evidence item to decide whether to verify, inspect, or return to the source dashboard.",
		Details: []string{
			fmt.Sprintf("Top evidence: %s risk=%d outcome=%s", top.ID, top.RiskScore, valueOrUnset(top.Outcome)),
		},
		Commands: commands,
	})
}

func mostActionableEvidence(records []EvidenceRecord) (EvidenceRecord, bool) {
	if len(records) == 0 {
		return EvidenceRecord{}, false
	}
	best := records[0]
	bestScore := evidenceActionabilityScore(best)
	for _, record := range records[1:] {
		score := evidenceActionabilityScore(record)
		if score > bestScore {
			best = record
			bestScore = score
		}
	}
	return best, true
}

func evidenceActionabilityScore(record EvidenceRecord) int {
	score := record.RiskScore
	if evidenceNeedsVerification(record) {
		score += 60
	}
	switch strings.ToLower(strings.TrimSpace(record.Kind)) {
	case "simulation_finding", "investigation_finding", "verification_failure", "fuzz_native_result":
		score += 20
	}
	return score
}

func evidenceNeedsVerification(record EvidenceRecord) bool {
	outcome := strings.ToLower(strings.TrimSpace(record.Outcome))
	severity := strings.ToLower(strings.TrimSpace(record.Severity))
	kind := strings.ToLower(strings.TrimSpace(record.Kind))
	return record.RiskScore >= 50 || outcome == "failed" || severity == "high" || strings.Contains(kind, "failure")
}

func evidenceSourceDashboardCommand(record EvidenceRecord) string {
	kind := strings.ToLower(strings.TrimSpace(record.Kind))
	signal := strings.ToLower(strings.TrimSpace(record.SignalClass))
	switch {
	case strings.Contains(kind, "fuzz") || strings.Contains(signal, "fuzz"):
		return "/fuzz-campaign"
	case strings.Contains(kind, "simulation") || strings.Contains(signal, "simulation"):
		return "/simulate-dashboard"
	case strings.Contains(kind, "investigation") || strings.Contains(signal, "investigation"):
		return "/investigate dashboard"
	case strings.Contains(kind, "analysis"):
		return "/analyze-dashboard"
	default:
		return ""
	}
}

func featureFuzzHandoff(feature FeatureWorkflow, campaign FuzzCampaign) string {
	if strings.TrimSpace(feature.ID) == "" || strings.TrimSpace(campaign.ID) == "" {
		return ""
	}
	if len(campaign.NativeResults) == 0 && len(campaign.SeedArtifacts) == 0 {
		return ""
	}
	nativeCount := len(campaign.NativeResults)
	findingCount := len(campaign.Findings)
	crashCount := 0
	hasFailed := false
	for _, result := range campaign.NativeResults {
		crashCount += result.CrashCount
		if fuzzCampaignNativeResultNeedsVerification(result) {
			hasFailed = true
		}
	}
	if hasFailed {
		return renderCommandHandoff("Feature fuzz", commandHandoffPlan{
			Title: "Native fuzz evidence should gate this tracked feature before close.",
			Details: []string{
				fmt.Sprintf("Campaign: %s findings=%d native_results=%d crashes=%d", campaign.ID, findingCount, nativeCount, crashCount),
			},
			Commands: []commandHandoffCommand{
				{Label: "Verify", Command: "/verify"},
				{Label: "Evidence", Command: "/evidence-search kind:fuzz_native_result"},
				{Label: "Campaign", Command: "/fuzz-campaign run"},
			},
		})
	}
	return renderCommandHandoff("Feature fuzz", commandHandoffPlan{
		Title: "Fuzz campaign context is available; keep it in the feature verification loop.",
		Details: []string{
			fmt.Sprintf("Campaign: %s findings=%d seeds=%d native_results=%d", campaign.ID, findingCount, len(campaign.SeedArtifacts), nativeCount),
		},
		Commands: []commandHandoffCommand{
			{Label: "Campaign", Command: "/fuzz-campaign run"},
			{Label: "Verify", Command: "/verify"},
		},
	})
}

func memoryHandoff(records []PersistentMemoryRecord) string {
	top, ok := mostActionableMemory(records)
	if !ok {
		return ""
	}
	commands := []commandHandoffCommand{
		{Label: "Explore", Command: "/mem-dashboard"},
	}
	if strings.TrimSpace(top.ID) != "" && top.Trust == PersistentMemoryTentative {
		commands = append([]commandHandoffCommand{{Label: "Confirm", Command: "/mem-confirm " + top.ID}}, commands...)
	}
	if strings.TrimSpace(top.ID) != "" && top.Importance != PersistentMemoryHigh && memoryShouldPromote(top) {
		commands = append(commands, commandHandoffCommand{Label: "Promote", Command: "/mem-promote " + top.ID})
	}
	if len(top.VerificationFailures) > 0 || top.VerificationMaxRisk >= 50 {
		commands = append([]commandHandoffCommand{{Label: "Verify", Command: "/verify"}}, commands...)
	}
	return renderCommandHandoff("Memory", commandHandoffPlan{
		Title: "Keep durable context trustworthy by confirming, promoting, or verifying the most actionable record.",
		Details: []string{
			fmt.Sprintf("Top memory: %s importance=%s trust=%s", top.ID, top.ImportanceLabel(), top.TrustLabel()),
		},
		Commands: commands,
	})
}

func memoryHandoffFromHits(hits []PersistentMemoryHit) string {
	records := make([]PersistentMemoryRecord, 0, len(hits))
	for _, hit := range hits {
		records = append(records, hit.Record)
	}
	return memoryHandoff(records)
}

func mostActionableMemory(records []PersistentMemoryRecord) (PersistentMemoryRecord, bool) {
	if len(records) == 0 {
		return PersistentMemoryRecord{}, false
	}
	best := records[0]
	bestScore := memoryActionabilityScore(best)
	for _, record := range records[1:] {
		score := memoryActionabilityScore(record)
		if score > bestScore {
			best = record
			bestScore = score
		}
	}
	return best, true
}

func memoryActionabilityScore(record PersistentMemoryRecord) int {
	score := record.VerificationMaxRisk
	if record.Trust == PersistentMemoryTentative {
		score += 50
	}
	if memoryShouldPromote(record) {
		score += 25
	}
	if len(record.VerificationFailures) > 0 {
		score += 35
	}
	return score
}

func memoryShouldPromote(record PersistentMemoryRecord) bool {
	return record.VerificationMaxRisk >= 50 || len(record.VerificationFailures) > 0 || len(record.VerificationArtifacts) > 0 || strings.Contains(strings.ToLower(record.Request), "analyze-project")
}

func checkpointHandoffAfterCreate(meta CheckpointMetadata) string {
	return renderCommandHandoff("Checkpoint", commandHandoffPlan{
		Title: "The rollback point is saved; inspect the current delta when you need confidence before continuing.",
		Details: []string{
			fmt.Sprintf("Checkpoint: %s files=%d", meta.ID, meta.FileCount),
		},
		Commands: []commandHandoffCommand{
			{Label: "Inspect", Command: "/checkpoint-diff latest"},
			{Label: "List", Command: "/checkpoints"},
		},
	})
}

func checkpointsHandoff(items []CheckpointMetadata) string {
	if len(items) == 0 {
		return ""
	}
	latest := items[0]
	return renderCommandHandoff("Checkpoint", commandHandoffPlan{
		Title: "Pick a checkpoint to compare before risky edits.",
		Details: []string{
			fmt.Sprintf("Latest: %s", latest.ID),
		},
		Commands: []commandHandoffCommand{
			{Label: "Compare", Command: "/checkpoint-diff " + latest.ID},
			{Label: "Verify", Command: "/verify"},
		},
	})
}

func checkpointDiffHandoff(meta CheckpointMetadata, diffs []CheckpointDiffEntry) string {
	if len(diffs) == 0 {
		return renderCommandHandoff("Checkpoint", commandHandoffPlan{
			Title:    "No file delta was found against this checkpoint.",
			Details:  []string{"Checkpoint: " + meta.ID},
			Commands: []commandHandoffCommand{{Label: "List", Command: "/checkpoints"}},
		})
	}
	return renderCommandHandoff("Checkpoint", commandHandoffPlan{
		Title: "Review the diff, then verify current changes before deciding the next recovery step.",
		Details: []string{
			fmt.Sprintf("Checkpoint: %s changed_files=%d", meta.ID, len(diffs)),
		},
		Commands: []commandHandoffCommand{
			{Label: "Verify", Command: "/verify"},
			{Label: "List", Command: "/checkpoints"},
		},
	})
}

func featureStatusHandoff(feature FeatureWorkflow) string {
	switch strings.ToLower(strings.TrimSpace(feature.Status)) {
	case featureStatusDraft, featureStatusPlanned:
		return renderCommandHandoff("Feature", commandHandoffPlan{
			Title:    "The feature is planned; let Kernforge execute the tracked implementation when ready.",
			Details:  []string{"Feature: " + feature.ID},
			Commands: []commandHandoffCommand{{Label: "Continue", Command: "/new-feature implement"}},
		})
	case featureStatusImplemented:
		return renderCommandHandoff("Feature", commandHandoffPlan{
			Title:   "The implementation exists; verify it before closing the tracked feature.",
			Details: []string{"Feature: " + feature.ID},
			Commands: []commandHandoffCommand{
				{Label: "Verify", Command: "/verify"},
				{Label: "Close", Command: "/new-feature close"},
			},
		})
	case featureStatusDone:
		return renderCommandHandoff("Feature", commandHandoffPlan{
			Title:   "The feature is closed; preserve or clean up the workspace state.",
			Details: []string{"Feature: " + feature.ID},
			Commands: []commandHandoffCommand{
				{Label: "Checkpoint", Command: "/checkpoint feature-done"},
				{Label: "Worktree", Command: "/worktree status"},
			},
		})
	case featureStatusBlocked:
		return renderCommandHandoff("Feature", commandHandoffPlan{
			Title:    "The feature is blocked; regenerate the plan or inspect current artifacts.",
			Details:  []string{"Feature: " + feature.ID},
			Commands: []commandHandoffCommand{{Label: "Replan", Command: "/new-feature plan"}},
		})
	default:
		return ""
	}
}

func featureCloseHandoff(feature FeatureWorkflow, hasWorktree bool) string {
	commands := []commandHandoffCommand{
		{Label: "Checkpoint", Command: "/checkpoint feature-done"},
	}
	if hasWorktree {
		commands = append([]commandHandoffCommand{{Label: "Cleanup", Command: "/worktree cleanup"}}, commands...)
	}
	return renderCommandHandoff("Feature", commandHandoffPlan{
		Title:    "The tracked feature is done; clean isolated state or preserve the final workspace.",
		Details:  []string{"Feature: " + feature.ID},
		Commands: commands,
	})
}

func worktreeHandoff(action string, activeFeatureID string) string {
	commands := []commandHandoffCommand{{Label: "Inspect", Command: "/worktree status"}}
	if strings.TrimSpace(activeFeatureID) != "" {
		commands = append([]commandHandoffCommand{{Label: "Feature", Command: "/new-feature status"}}, commands...)
	}
	return renderCommandHandoff("Worktree", commandHandoffPlan{
		Title:    "Keep the isolated workspace aligned with the active task.",
		Commands: commands,
	})
}

func specialistAssignHandoff(nodeID string, activeFeatureID string) string {
	commands := []commandHandoffCommand{{Label: "Inspect", Command: "/specialists status"}}
	if strings.TrimSpace(activeFeatureID) != "" {
		commands = append([]commandHandoffCommand{{Label: "Feature", Command: "/new-feature status"}}, commands...)
	}
	return renderCommandHandoff("Specialist", commandHandoffPlan{
		Title:    "The editable owner is pinned; continue through the tracked workflow or inspect routing.",
		Details:  []string{"Node: " + strings.TrimSpace(nodeID)},
		Commands: commands,
	})
}
