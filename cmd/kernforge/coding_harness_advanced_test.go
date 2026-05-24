package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func addArtifactQualitySourceEvidence(session *Session, path string) {
	session.Messages = append(session.Messages, Message{
		Role:     "tool",
		ToolName: "read_file",
		Text:     "source excerpt",
		ToolMeta: map[string]any{
			"path": path,
		},
	})
}

func TestArtifactQualityTargetsIgnoreArchivedPatchFromPreviousTurn(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{
		{
			Role: "user",
			Text: "Tavern/BugReport.md를 작성해",
		},
		{
			Role:  "assistant",
			Phase: messagePhaseFinalAnswer,
			Text:  "보고서 작성 완료",
		},
		{
			Role: "user",
			Text: "현재 상태만 알려줘",
		},
	}
	session.PatchTransactions = []PatchTransaction{{
		ID:     "patch-doc-old",
		Goal:   "Tavern/BugReport.md를 작성해",
		Status: patchTransactionStatusCommitted,
		Entries: []PatchTransactionEntry{{
			ID:       "patch-doc-old-001",
			ToolName: "write_file",
			Status:   "success",
			Paths: []PatchPathChange{{
				Path:      "Tavern/BugReport.md",
				Operation: "write_file",
			}},
		}},
	}}

	targets := collectArtifactQualityTargets(session, "")
	if len(targets) != 0 {
		t.Fatalf("expected artifact quality to ignore previous-turn document patch, got %#v", targets)
	}
}

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

func TestArtifactQualityWarnsSourceReviewReportWithoutSourceEvidence(t *testing.T) {
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
		"총 1개 버그를 문서화했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| High | 1 |",
		"| Total | 1 |",
		"",
		"## BUG-001",
		"- File: Tavern.cpp",
		"- Impact: documented issue.",
	}, "\n")
	if err := os.WriteFile(reportPath, []byte(reportText), 0o644); err != nil {
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

	report := agent.buildCodingHarnessReport("Created Tavern/BugReport.md with 1 bug. Verification was not run.", false, false)
	if !report.Approved {
		t.Fatalf("expected missing source evidence to warn without blocking document finalization: %s", report.BlockingFeedback())
	}
	rendered := report.ArtifactQuality.RenderPromptSection()
	if !strings.Contains(rendered, "Source review artifact has no source inspection evidence") {
		t.Fatalf("expected source evidence warning, got %q", rendered)
	}
}

func TestArtifactQualityRecordsSourceEvidenceForSourceReviewReport(t *testing.T) {
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
		"총 1개 버그를 문서화했습니다.",
		"",
		"| Severity | Count |",
		"|----------|-------|",
		"| High | 1 |",
		"| Total | 1 |",
		"",
		"## BUG-001",
		"- File: Tavern/Tavern.cpp",
		"- Impact: documented issue.",
	}, "\n")
	if err := os.WriteFile(reportPath, []byte(reportText), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	request := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 문서로 생성해"
	contract := buildAcceptanceContract(request, TurnIntentEditCode, false, true, false)
	session := NewSession(root, "scripted", "model", "", "default")
	session.AcceptanceContract = &contract
	session.Messages = []Message{{Role: "user", Text: request}}
	session.Messages = append(session.Messages,
		Message{
			Role:     "tool",
			ToolName: "read_file",
			Text:     "report excerpt",
			ToolMeta: map[string]any{"path": "Tavern/BugReport.md"},
		},
		Message{
			Role:     "tool",
			ToolName: "list_files",
			Text:     "Tavern/BugReport.md",
			ToolMeta: map[string]any{"path": ".kernforge/reviews"},
		},
	)
	addArtifactQualitySourceEvidence(session, "Tavern/Tavern.cpp")
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

	report := agent.buildCodingHarnessReport("Created Tavern/BugReport.md with 1 bug. Verification was not run.", false, false)
	if !report.Approved {
		t.Fatalf("expected source evidence to satisfy artifact quality: %s", report.BlockingFeedback())
	}
	if got := strings.Join(report.ArtifactQuality.SourceEvidence, "\n"); got != "read_file:Tavern/Tavern.cpp" {
		t.Fatalf("expected only non-artifact source evidence, got %q", got)
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
	addArtifactQualitySourceEvidence(session, "Tavern.cpp")
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
	addArtifactQualitySourceEvidence(session, "Tavern.cpp")
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

func TestArtifactQualityExpandsBugIDRanges(t *testing.T) {
	text := strings.Join([]string{
		"# Bug Report",
		"",
		"Total 27 documented bugs.",
		"",
		"Covered range: BUG-001 through BUG-027.",
	}, "\n")

	profile := analyzeBugReportDocumentCounts(text)
	if profile.UniqueBugIDs != 27 {
		t.Fatalf("expected BUG range to expand to 27 IDs, got %#v", profile)
	}
	if len(profile.TotalClaims) != 1 || profile.TotalClaims[0] != 27 {
		t.Fatalf("expected total claim 27, got %#v", profile.TotalClaims)
	}
	if len(profile.Conflicts) > 0 {
		t.Fatalf("expected no conflicts for matching range and total, got %#v", profile.Conflicts)
	}
}

func TestArtifactQualityAllowsSeveritySummaryBugIDRanges(t *testing.T) {
	text := strings.Join([]string{
		"# Bug Report",
		"",
		"Total 5 documented bugs.",
		"",
		"| Severity | Count | Bug IDs |",
		"|----------|-------|---------|",
		"| Critical | 5 | BUG-001 through BUG-005 |",
	}, "\n")

	profile := analyzeBugReportDocumentCounts(text)
	if profile.UniqueBugIDs != 5 || profile.SeverityTotal != 5 {
		t.Fatalf("expected matching BUG range and severity total, got %#v", profile)
	}
	if len(profile.Conflicts) > 0 {
		t.Fatalf("expected severity BUG range not to conflict, got %#v", profile.Conflicts)
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
	addArtifactQualitySourceEvidence(session, "Tavern.cpp")
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	report := agent.buildCodingHarnessReport("Created Tavern/BugReport.md with 2 bugs. Build/test verification was not run.", false, false)
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
	addArtifactQualitySourceEvidence(session, "Tavern.cpp")
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

func TestGeneratedDocumentFinalAnswerBugRangeMismatchIsAnswerOnly(t *testing.T) {
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
		"총 27개 버그를 문서화했습니다.",
		"",
		"Covered range: BUG-001 through BUG-027.",
	}
	for i := 1; i <= 27; i++ {
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
	addArtifactQualitySourceEvidence(session, "Tavern.cpp")
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
		"The report documents 26 documented bugs in Tavern/BugReport.md.",
		"Covered range: BUG-001 through BUG-027.",
		"",
		"Build/test verification was not run because this is a documentation-only artifact.",
	}, "\n")
	report := agent.buildCodingHarnessReport(reply, false, false)
	if report.Approved {
		t.Fatalf("expected final answer range/count mismatch to block approval")
	}
	if codingHarnessFindingsHaveBlockers(report.ArtifactQuality.Findings) {
		t.Fatalf("expected artifact content to remain accepted, got %#v", report.ArtifactQuality.Findings)
	}
	if !strings.Contains(report.BlockingFeedback(), "Final answer has inconsistent bug counts") {
		t.Fatalf("expected final-answer range/count blocker, got %q", report.BlockingFeedback())
	}
	if !agent.shouldSynthesizeGeneratedDocumentArtifactFinalReply(request, &report, false) {
		t.Fatalf("expected document artifact range mismatch to synthesize a safe final answer")
	}
}

func TestGeneratedDocumentFinalAnswerSeverityListMismatchIsAnswerOnly(t *testing.T) {
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
		"총 5개 버그를 문서화했습니다.",
		"",
		"| Severity | Count | Bug IDs |",
		"|----------|-------|---------|",
		"| Critical | 5 | BUG-001, 002, 003, 020, 024 |",
		"| Total | 5 |",
		"",
	}
	for _, id := range []string{"001", "002", "003", "020", "024"} {
		reportLines = append(reportLines,
			"## BUG-"+id,
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
	addArtifactQualitySourceEvidence(session, "Tavern.cpp")
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
		"The report is complete in Tavern/BugReport.md.",
		"",
		"Critical: 4 (BUG-001, 002, 003, 020, 024)",
		"Total: 5 bugs",
		"",
		"Build/test verification was not run because this is a documentation-only artifact.",
	}, "\n")
	report := agent.buildCodingHarnessReport(reply, false, false)
	if report.Approved {
		t.Fatalf("expected final answer severity list mismatch to block approval")
	}
	if codingHarnessFindingsHaveBlockers(report.ArtifactQuality.Findings) {
		t.Fatalf("expected artifact content to remain accepted, got %#v", report.ArtifactQuality.Findings)
	}
	if !strings.Contains(report.BlockingFeedback(), "Final answer has inconsistent bug counts") {
		t.Fatalf("expected final-answer severity-list blocker, got %q", report.BlockingFeedback())
	}
	if !agent.shouldSynthesizeGeneratedDocumentArtifactFinalReply(request, &report, false) {
		t.Fatalf("expected document artifact final-answer mismatch to synthesize a safe final answer")
	}
}

func TestGeneratedDocumentFinalAnswerTopLevelAnswerOnlyBlockerSynthesizes(t *testing.T) {
	root := t.TempDir()
	request := "각 소스코드 파일들을 검토해서 버그를 찾아서 Tavern/BugReport.md 문서로 생성해"
	session := NewSession(root, "scripted", "model", "", "default")
	session.Messages = []Message{{Role: "user", Text: request}}
	contract := buildAcceptanceContract(request, TurnIntentEditCode, false, true, false)
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
	report := CodingHarnessReport{
		Approved: false,
		Findings: []CodingHarnessFinding{{
			Severity: "blocker",
			Title:    "Verification claim has no recorded evidence",
			Detail:   "The final answer claims verification passed, but no verification evidence was recorded.",
		}},
		ArtifactQuality: ArtifactQualityReport{
			Artifacts: []ArtifactQualityCheck{{
				Path:         "Tavern/BugReport.md",
				Kind:         "document",
				Substantive:  true,
				ContentChars: 512,
				Checks:       []string{"document artifact exists", "substantive content"},
			}},
		},
	}

	if !codingHarnessReportRequiresFinalAnswerOnlyRevision(&report) {
		t.Fatalf("expected top-level blocker to be final-answer-only")
	}
	if !agent.shouldSynthesizeGeneratedDocumentArtifactFinalReply(request, &report, false) {
		t.Fatalf("expected accepted document artifact with top-level answer-only blocker to synthesize a safe final reply")
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
