package main

import (
	"fmt"
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

func TestArtifactQualityBlocksBugReportCountMismatch(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "Tavern", "BugReport.md")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	reportText := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"총 3개 버그를 문서화했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 1 |",
		"| High | 1 |",
		"| Total | 2 |",
		"",
		"## BUG-001",
		"- File: Tavern.cpp",
		"",
		"## BUG-002",
		"- File: RuntimeManager.cpp",
		"",
		"## BUG-003",
		"- File: TavernWorkerManager.cpp",
	}, "\n")
	if err := os.WriteFile(reportPath, []byte(reportText), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	contract := buildAcceptanceContract("각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 문서로 생성해", TurnIntentEditCode, false, true, false)
	session := NewSession(root, "scripted", "model", "", "default")
	session.AcceptanceContract = &contract
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-doc",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:       "patch-doc-001",
			ToolName: "write_file",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "write_file",
			}},
		}},
	}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	report := agent.buildCodingHarnessReport("Created Tavern/BugReport.md with 3 bugs.", false, false)
	if report.Approved {
		t.Fatalf("expected inconsistent bug report counts to block approval: %#v", report.ArtifactQuality)
	}
	feedback := report.BlockingFeedback()
	for _, want := range []string{
		"Artifact total does not match bug IDs",
		"Artifact severity summary does not match bug IDs",
	} {
		if !strings.Contains(feedback, want) {
			t.Fatalf("expected %q in feedback, got %q", want, feedback)
		}
	}
}

func TestArtifactQualityBlocksBugReportDocumentedClaimAndAbbreviatedSeverityListMismatch(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "Tavern", "BugReport.md")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	reportLines := []string{
		"# Tavern Bug Report",
		"",
		"- **27 documented bugs**",
		"",
		"| Severity | Count | Bug IDs |",
		"|----------|-------|---------|",
		"| Critical | 4 | BUG-001, 002, 003, 020, 024 |",
		"| High | 7 | BUG-004, 005, 006, 007, 008, 009, 010 |",
		"| Medium | 9 | BUG-011, 012, 013, 014, 015, 016, 017, 018, 019 |",
		"| Low | 6 | BUG-020, 021, 022, 023, 024, 025 |",
		"",
	}
	for i := 1; i <= 26; i++ {
		reportLines = append(reportLines,
			fmt.Sprintf("## BUG-%03d", i),
			"- File: sample.cpp",
			"- Impact: documented issue.",
			"",
		)
	}
	if err := os.WriteFile(reportPath, []byte(strings.Join(reportLines, "\n")), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	contract := buildAcceptanceContract("각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 문서로 생성해", TurnIntentEditCode, false, true, false)
	session := NewSession(root, "scripted", "model", "", "default")
	session.AcceptanceContract = &contract
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-doc",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:       "patch-doc-001",
			ToolName: "write_file",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "write_file",
			}},
		}},
	}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	report := agent.buildCodingHarnessReport("Created Tavern/BugReport.md with 27 documented bugs.", false, false)
	if report.Approved {
		t.Fatalf("expected inconsistent documented bug report counts to block approval: %#v", report.ArtifactQuality)
	}
	feedback := report.BlockingFeedback()
	for _, want := range []string{
		"Artifact total does not match bug IDs",
		"critical severity row claims 4 but lists 5 BUG IDs",
	} {
		if !strings.Contains(feedback, want) {
			t.Fatalf("expected %q in feedback, got %q", want, feedback)
		}
	}
}

func TestArtifactQualityAllowsSeveritySummaryBugIDsWithLineNumbers(t *testing.T) {
	text := strings.Join([]string{
		"# Bug Report",
		"",
		"1 documented bug",
		"",
		"| Severity | Count | Bug IDs |",
		"|----------|-------|---------|",
		"| Critical | 1 | BUG-001 at RuntimeManager.cpp:120 |",
		"| Total | 1 |",
		"",
		"## BUG-001",
		"- File: RuntimeManager.cpp:120",
	}, "\n")

	profile := analyzeBugReportDocumentCounts(text)
	if len(profile.Conflicts) > 0 {
		t.Fatalf("expected line numbers near BUG IDs not to be counted as abbreviated IDs, got %#v", profile.Conflicts)
	}
	if profile.UniqueBugIDs != 1 || profile.SeverityTotal != 1 {
		t.Fatalf("expected one bug and one severity count, got %#v", profile)
	}
}

func TestArtifactQualityAllowsSeverityBugIDListWithoutCountColumn(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "Tavern", "BugReport.md")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	reportText := strings.Join([]string{
		"# Tavern Bug Report",
		"",
		"각 소스코드 파일들을 검토해서 버그를 찾아서 생성한 별도 문서입니다.",
		"",
		"총 2개 버그를 문서화했습니다.",
		"",
		"| Severity | Bug IDs |",
		"|----------|---------|",
		"| Critical | BUG-001, BUG-002 |",
		"| Total | 2 |",
		"",
		"## BUG-001",
		"- File: Tavern.cpp",
		"- Impact: crash risk.",
		"",
		"## BUG-002",
		"- File: RuntimeManager.cpp",
		"- Impact: resource lifetime bug.",
	}, "\n")
	if err := os.WriteFile(reportPath, []byte(reportText), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	contract := buildAcceptanceContract("각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 문서로 생성해", TurnIntentEditCode, false, true, false)
	session := NewSession(root, "scripted", "model", "", "default")
	session.AcceptanceContract = &contract
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-doc",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:       "patch-doc-001",
			ToolName: "write_file",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "write_file",
			}},
		}},
	}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	report := agent.buildCodingHarnessReport("Created Tavern/BugReport.md with 2 bugs.", false, false)
	if !report.Approved {
		t.Fatalf("expected bug ID list table to pass without count-column false positive: %s", report.BlockingFeedback())
	}
}

func TestGeneratedDocumentFinalAnswerCountMismatchIsAnswerOnly(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "Tavern", "BugReport.md")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	reportLines := []string{
		"# Tavern Bug Report",
		"",
		"각 소스코드 파일들을 검토해서 버그를 찾아서 생성한 별도 문서입니다.",
		"",
		"총 26개 버그를 문서화했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 4 |",
		"| High | 7 |",
		"| Medium | 9 |",
		"| Low | 6 |",
		"| Total | 26 |",
		"",
	}
	for i := 1; i <= 26; i++ {
		reportLines = append(reportLines,
			fmt.Sprintf("## BUG-%03d", i),
			"- File: sample.cpp",
			"- Impact: documented issue.",
			"",
		)
	}
	if err := os.WriteFile(reportPath, []byte(strings.Join(reportLines, "\n")), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	request := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 문서로 생성해"
	contract := buildAcceptanceContract(request, TurnIntentEditCode, false, true, false)
	session := NewSession(root, "scripted", "model", "", "default")
	session.AcceptanceContract = &contract
	session.Messages = []Message{{Role: "user", Text: request}}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-doc",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:       "patch-doc-001",
			ToolName: "write_file",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "write_file",
			}},
		}},
	}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	reply := strings.Join([]string{
		"The report documents 27 documented bugs in Tavern/BugReport.md.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| Critical | 4 |",
		"| High | 7 |",
		"| Medium | 9 |",
		"| Low | 6 |",
		"| **Total** | **26** |",
		"",
		"Build/test verification was not run because this is a documentation-only artifact.",
	}, "\n")
	report := agent.buildCodingHarnessReport(reply, false, false)
	if report.Approved {
		t.Fatalf("expected final answer count mismatch to block approval")
	}
	if codingHarnessFindingsHaveBlockers(report.ArtifactQuality.Findings) {
		t.Fatalf("expected artifact content to remain accepted, got %#v", report.ArtifactQuality.Findings)
	}
	if !strings.Contains(report.BlockingFeedback(), "Final answer has inconsistent bug counts") {
		t.Fatalf("expected final-answer count blocker, got %q", report.BlockingFeedback())
	}
	if !agent.shouldSynthesizeGeneratedDocumentArtifactFinalReply(request, &report, false) {
		t.Fatalf("expected document artifact reply mismatch to synthesize a safe final answer instead of entering review")
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
