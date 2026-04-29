package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestArtifactQualityBlocksPlaceholderReport(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "report.md"), []byte("TODO: fill this in\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	contract := buildAcceptanceContract("create docs/report.md report about Win32 service stop handling", TurnIntentEditCode, false, true, false)
	session := NewSession(root, "scripted", "model", "", "default")
	session.AcceptanceContract = &contract
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	report := agent.buildCodingHarnessReport("Created docs/report.md.", false, false)
	if report.Approved {
		t.Fatalf("expected placeholder report artifact to block approval: %#v", report.ArtifactQuality)
	}
	if !strings.Contains(report.BlockingFeedback(), "Artifact contains placeholder content") {
		t.Fatalf("expected artifact quality blocker, got %q", report.BlockingFeedback())
	}
}

func TestArtifactQualityAllowsPathOnlyArtifactRequest(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "required.md"), []byte("# Required\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	contract := buildAcceptanceContract("create docs/required.md", TurnIntentEditCode, false, true, false)
	session := NewSession(root, "scripted", "model", "", "default")
	session.AcceptanceContract = &contract
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	report := agent.buildCodingHarnessReport("Created docs/required.md. Verification not run.", false, false)
	if !report.Approved {
		t.Fatalf("expected path-only artifact request to pass quality gate, got %s", report.BlockingFeedback())
	}
}

func TestScenarioReplayBlocksFixedClaimWithoutScenarioStatus(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	contract := buildAcceptanceContract("In party system, when invite and kick members repeatedly, expected party limit blocks extra invite, but observed extra member can be invited.", TurnIntentEditCode, false, true, false)
	session.AcceptanceContract = &contract
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-tx-test",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:       "entry-1",
			ToolName: "write_file",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "party.go",
				Operation: "update",
				After: HarnessFileFingerprint{
					Path:   "party.go",
					Kind:   "file",
					Exists: true,
				},
			}},
		}},
	}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	report := agent.buildCodingHarnessReport("Fixed the issue.", false, false)
	if report.Approved {
		t.Fatalf("expected missing scenario replay status to block approval, scenario=%#v", report.ScenarioReplay)
	}
	if !strings.Contains(report.BlockingFeedback(), "Scenario replay outcome is missing") {
		t.Fatalf("expected scenario replay blocker, got %q", report.BlockingFeedback())
	}

	report = agent.buildCodingHarnessReport("Fixed the party invite/kick limit issue. Scenario replay not run.", false, false)
	if !report.Approved {
		t.Fatalf("expected explicit scenario replay not-run status to pass, got %q", report.BlockingFeedback())
	}
}

func TestScenarioReplayIgnoresInstructionalIfWithoutObservedBridge(t *testing.T) {
	prompt := "Implement the requested feature.\n\nExecution requirements:\nIf you cannot finish cleanly, explain the blocker and the remaining work.\nRun relevant verification before finishing when practical."
	if scenarioReplayPromptLooksRelevant(prompt) {
		t.Fatalf("expected generic execution boilerplate to be ignored by scenario replay")
	}
}

func TestScenarioReplayKeepsExplicitScenarioInsideExecutionPrompt(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{
		ID:           "accept-scenario",
		SourcePrompt: "Implement the fix.\n\nExecution requirements:\nIf you cannot finish cleanly, explain the blocker.\n\nScenario: when the service receives sc stop, expected process exits, but observed process remains running.",
		Mode:         "edit_code",
	}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	report := agent.buildScenarioReplayReport("Fixed.")
	if len(report.Scenarios) != 1 {
		t.Fatalf("expected explicit scenario to survive execution boilerplate, got %#v", report)
	}
	if len(report.Findings) == 0 {
		t.Fatalf("expected fixed claim without scenario bridge to be flagged")
	}
}

func TestSubagentOrchestrationBlocksWeakRootCauseWorkerEvidence(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	contract := buildAcceptanceContract("Find root cause: when a document file is requested, expected file creation, but observed missing file.", TurnIntentAskProjectKnowledge, true, false, false)
	session.AcceptanceContract = &contract
	session.TaskGraph = &TaskGraph{Nodes: []TaskNode{{
		ID:               "plan-01",
		Title:            "Inspect document creation path",
		Kind:             "inspection",
		Status:           "completed",
		MicroWorkerBrief: "Maybe config problem.",
	}}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	report := agent.buildCodingHarnessReport("Root cause is config.", false, false)
	if report.Approved {
		t.Fatalf("expected weak worker root-cause evidence to block approval")
	}
	if !strings.Contains(report.BlockingFeedback(), "Worker evidence lacks causal validation") {
		t.Fatalf("expected subagent orchestration blocker, got %q", report.BlockingFeedback())
	}
}

func TestSubagentOrchestrationIgnoresWeakWorkerEvidenceForNonRootCausePrompt(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.AcceptanceContract = &AcceptanceContract{
		ID:           "accept-explain",
		SourcePrompt: "Explain the repository build layout.",
		Mode:         "analysis_only",
	}
	session.TaskGraph = &TaskGraph{Nodes: []TaskNode{{
		ID:               "plan-01",
		Title:            "Inspect build layout",
		Kind:             "inspection",
		Status:           "completed",
		MicroWorkerBrief: "Maybe config problem.",
	}}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	report := agent.buildSubagentOrchestrationReport("The cause is the build settings.")
	if len(report.Findings) != 0 {
		t.Fatalf("expected non-root-cause prompt to ignore weak stale worker evidence, got %#v", report.Findings)
	}
}

func TestSubagentOrchestrationBlocksUndisclosedRootCauseReviewFailures(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.LastAnalysis = &ProjectAnalysisSummary{
		RunID:          "analysis-1",
		Goal:           "Find root cause",
		Mode:           "root-cause",
		Status:         "completed_with_review_failures",
		AgentCount:     2,
		ApprovedShards: 1,
		ReviewFailures: 1,
		TotalShards:    2,
		StartedAt:      time.Now(),
		CompletedAt:    time.Now(),
	}
	session.AcceptanceContract = &AcceptanceContract{
		ID:           "accept-root",
		SourcePrompt: "Find root cause: when sc stop runs, expected service exits, but observed process remains running.",
		Mode:         "analysis_only",
	}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	report := agent.buildCodingHarnessReport("Root cause is the stop handler.", false, false)
	if report.Approved {
		t.Fatalf("expected undisclosed review failure to block approval")
	}
	if !strings.Contains(report.BlockingFeedback(), "Root-cause reviewer issues are not disclosed") {
		t.Fatalf("expected review failure blocker, got %q", report.BlockingFeedback())
	}

	report = agent.buildCodingHarnessReport("Root cause is the stop handler. Reduced confidence: review failures remain.", false, false)
	if !report.Approved {
		t.Fatalf("expected disclosed review failure to pass, got %q", report.BlockingFeedback())
	}
}
