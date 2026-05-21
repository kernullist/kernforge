package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditLoopRecordsApplyVerifyRetryAndFinalReview(t *testing.T) {
	session := NewSession("C:\\workspace", "scripted", "model", "", "default")
	session.RecordEditLoopEvent("fix unstable parser", EditLoopEvent{
		Kind:         "worker_apply",
		Source:       "parallel-worker",
		ToolName:     "apply_patch",
		Summary:      "updated parser guard",
		Status:       "applied",
		ChangedPaths: []string{"parser.go"},
	})
	session.RecordEditLoopEvent("fix unstable parser", EditLoopEvent{
		Kind:    "verification",
		Source:  "auto",
		Summary: "go test ./...",
		Status:  "failed",
		Detail:  "parser_test still fails",
	})
	session.RecordEditLoopEvent("fix unstable parser", EditLoopEvent{
		Kind:    "retry",
		Source:  "controller",
		Summary: "retry after parser fix",
		Status:  "planned",
	})
	session.RecordEditLoopEvent("fix unstable parser", EditLoopEvent{
		Kind:    "verification",
		Source:  "auto",
		Summary: "go test ./... passed",
		Status:  "passed",
	})
	session.RecordEditLoopEvent("fix unstable parser", EditLoopEvent{
		Kind:    "final_review",
		Source:  "reviewer",
		Summary: "APPROVED",
		Status:  "approved",
	})

	if session.ActiveEditLoop == nil {
		t.Fatalf("expected active edit loop")
	}
	loop := session.ActiveEditLoop
	if loop.AttemptCount != 1 {
		t.Fatalf("expected one apply attempt, got %#v", loop)
	}
	if loop.RetryCount != 1 {
		t.Fatalf("expected one retry, got %#v", loop)
	}
	if loop.VerificationStatus != "passed" {
		t.Fatalf("expected final verification status to pass, got %#v", loop)
	}
	if loop.FinalReviewVerdict != "approved" {
		t.Fatalf("expected final review verdict, got %#v", loop)
	}
	if len(loop.RemainingRisks) != 0 {
		t.Fatalf("expected passed verification to clear stale verification risks, got %#v", loop.RemainingRisks)
	}
	rendered := loop.RenderPromptSection()
	for _, want := range []string{"parser.go", "Retry count: 1", "Final review: approved"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered edit loop to include %q, got:\n%s", want, rendered)
		}
	}
	if !strings.Contains(rendered, "Worker evidence: 1 recorded") || !strings.Contains(rendered, "Verification evidence: 2 recorded") {
		t.Fatalf("expected rendered edit loop to include evidence counts, got:\n%s", rendered)
	}
}

func TestEditLoopStartsFreshWhenGoalChanges(t *testing.T) {
	session := NewSession("C:\\workspace", "scripted", "model", "", "default")
	session.RecordEditLoopEvent("fix alpha", EditLoopEvent{
		Kind:         "apply",
		Source:       "main",
		ToolName:     "write_file",
		Summary:      "updated alpha",
		Status:       "ok",
		ChangedPaths: []string{"alpha.go"},
	})
	firstID := session.ActiveEditLoop.ID

	session.RecordEditLoopEvent("fix beta", EditLoopEvent{
		Kind:         "apply",
		Source:       "main",
		ToolName:     "write_file",
		Summary:      "updated beta",
		Status:       "ok",
		ChangedPaths: []string{"beta.go"},
	})

	if session.ActiveEditLoop == nil {
		t.Fatalf("expected a fresh active edit loop")
	}
	if session.ActiveEditLoop.ID == firstID {
		t.Fatalf("expected a new edit loop id after goal change, got %#v", session.ActiveEditLoop)
	}
	if session.ActiveEditLoop.Goal != "fix beta" {
		t.Fatalf("expected new active goal, got %#v", session.ActiveEditLoop)
	}
	if !containsString(session.ActiveEditLoop.ChangedPaths, "beta.go") {
		t.Fatalf("expected beta path in fresh loop, got %#v", session.ActiveEditLoop.ChangedPaths)
	}
	if len(session.EditLoops) == 0 {
		t.Fatalf("expected old loop to be archived")
	}
	if session.EditLoops[0].ID != firstID || session.EditLoops[0].Status != editLoopStatusRiskAccepted {
		t.Fatalf("expected old loop to be archived as risk accepted, got %#v", session.EditLoops)
	}
}

func TestPromptActiveEditLoopKeepsStatusContextWithoutFinalGateScope(t *testing.T) {
	session := NewSession("C:\\workspace", "scripted", "model", "", "default")
	session.Messages = []Message{
		{Role: "user", Text: "fix alpha"},
		{Role: "user", Text: "현재 상태만 알려줘"},
	}
	session.ActiveEditLoop = &EditLoopState{
		ID:           "edit-loop-alpha",
		Goal:         "fix alpha",
		Status:       editLoopStatusActive,
		ChangedPaths: []string{"alpha.go"},
	}

	if currentTurnActiveEditLoop(session) != nil {
		t.Fatalf("status-only follow-up must not count stale edit loop as current-turn gate evidence")
	}
	if promptActiveEditLoop(session) == nil {
		t.Fatalf("status-only follow-up should still receive active edit-loop context for answering")
	}
}

func TestSessionExportIncludesEditLoopLedger(t *testing.T) {
	session := NewSession("C:\\workspace", "scripted", "model", "", "default")
	session.RecordEditLoopEvent("fix file", EditLoopEvent{
		Kind:         "apply",
		Source:       "main",
		ToolName:     "write_file",
		Summary:      "wrote main.go",
		Status:       "ok",
		ChangedPaths: []string{"main.go"},
	})
	session.FinalizeActiveEditLoop(editLoopStatusCompleted)

	exported := session.ExportText()
	if !strings.Contains(exported, "## Recent Edit Loops") || !strings.Contains(exported, "main.go") {
		t.Fatalf("expected exported session to include finalized edit loop, got:\n%s", exported)
	}
	if !strings.Contains(exported, "Active permission profile: :workspace") {
		t.Fatalf("expected exported session to include permission profile provenance, got:\n%s", exported)
	}
}

func TestClosedEditLoopDoesNotDriveOutcomeHarness(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.RecordEditLoopEvent("old fix", EditLoopEvent{
		Kind:         "apply",
		Source:       "main",
		ToolName:     "write_file",
		Summary:      "old edit",
		Status:       "ok",
		ChangedPaths: []string{"old.go"},
	})
	session.RecordEditLoopEvent("old fix", EditLoopEvent{
		Kind:    "risk",
		Source:  "controller",
		Summary: "Old turn had unresolved verification.",
		Status:  "open",
	})
	session.FinalizeActiveEditLoop(editLoopStatusRiskAccepted)

	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}
	report := agent.buildOutcomeInvariantReport("No remaining blockers for this explanation-only answer.", false)
	for _, finding := range report.Findings {
		if strings.Contains(finding.Title, "edit-loop risk") {
			t.Fatalf("closed edit loop should not affect a later final answer, got %#v", report.Findings)
		}
	}
}

func TestFinalizeEditLoopKeepsFailedApplyRisk(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.RecordEditLoopEvent("fix file", EditLoopEvent{
		Kind:     "apply",
		Source:   "main",
		ToolName: "write_file",
		Summary:  "write_file failed for missing path",
		Status:   "error",
	})
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}

	agent.finalizeEditLoopOnReturn("Could not apply the edit; verification was not run.", false)

	if session.ActiveEditLoop != nil {
		t.Fatalf("expected failed apply loop to finalize, got %#v", session.ActiveEditLoop)
	}
	if len(session.EditLoops) == 0 {
		t.Fatalf("expected finalized edit loop")
	}
	loop := session.EditLoops[0]
	if loop.Status != editLoopStatusRiskAccepted {
		t.Fatalf("expected risk-accepted status for failed apply, got %#v", loop)
	}
	if len(loop.RemainingRisks) == 0 {
		t.Fatalf("expected failed apply risk to remain, got %#v", loop)
	}
}

func TestFinalizeEditLoopRecordsMissingVerificationRiskWhenDisclosed(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.RecordEditLoopEvent("fix file", EditLoopEvent{
		Kind:         "apply",
		Source:       "main",
		ToolName:     "write_file",
		Summary:      "wrote main.go",
		Status:       "ok",
		ChangedPaths: []string{"main.go"},
	})
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}

	agent.finalizeEditLoopOnReturn("Updated main.go. Verification was not run.", false)

	if len(session.EditLoops) == 0 {
		t.Fatalf("expected finalized edit loop")
	}
	loop := session.EditLoops[0]
	if loop.Status != editLoopStatusRiskAccepted {
		t.Fatalf("missing verification evidence must leave risk-accepted status, got %#v", loop)
	}
	if !containsStringMatching(loop.RemainingRisks, "No successful verification") {
		t.Fatalf("expected missing verification risk, got %#v", loop.RemainingRisks)
	}
}

func TestFinalizeEditLoopKeepsSkippedShellVerificationRisk(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}
	session.RecordEditLoopEvent("fix file", EditLoopEvent{
		Kind:         "apply",
		Source:       "main",
		ToolName:     "write_file",
		Summary:      "wrote main.go",
		Status:       "ok",
		ChangedPaths: []string{"main.go"},
	})

	agent.recordEditLoopToolResult(ToolCall{Name: "run_shell"}, ToolExecutionResult{
		DisplayText: "Verification skipped by user.",
		Meta: map[string]any{
			"verification_like":        true,
			"verification_status":      string(VerificationSkipped),
			"command_execution_status": "declined",
			"command":                  "go test ./...",
			"success":                  false,
		},
	}, nil)

	if session.ActiveEditLoop == nil {
		t.Fatalf("expected active edit loop")
	}
	if session.ActiveEditLoop.VerificationStatus != "skipped" {
		t.Fatalf("expected skipped verification status, got %#v", session.ActiveEditLoop)
	}
	if !containsStringMatching(session.ActiveEditLoop.RemainingRisks, "skipped") {
		t.Fatalf("expected skipped verification risk, got %#v", session.ActiveEditLoop.RemainingRisks)
	}

	agent.finalizeEditLoopOnReturn("Updated main.go. Verification was skipped.", false)

	if len(session.EditLoops) == 0 {
		t.Fatalf("expected finalized edit loop")
	}
	loop := session.EditLoops[0]
	if loop.Status != editLoopStatusRiskAccepted {
		t.Fatalf("skipped verification must leave risk-accepted status, got %#v", loop)
	}
	if !containsStringMatching(loop.RemainingRisks, "No successful verification") {
		t.Fatalf("expected missing successful verification risk, got %#v", loop.RemainingRisks)
	}
}

func TestEditLoopCommandExecutionDeclinedDoesNotPassVerification(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}
	session.RecordEditLoopEvent("fix file", EditLoopEvent{
		Kind:         "apply",
		Source:       "main",
		ToolName:     "write_file",
		Summary:      "wrote main.go",
		Status:       "ok",
		ChangedPaths: []string{"main.go"},
	})

	agent.recordEditLoopToolResult(ToolCall{Name: "run_shell"}, ToolExecutionResult{
		DisplayText: "Build verification was declined.",
		Meta: map[string]any{
			"verification_like":        true,
			"command_execution_status": "declined",
			"command":                  "go test ./...",
		},
	}, nil)

	if session.ActiveEditLoop == nil {
		t.Fatalf("expected active edit loop")
	}
	if session.ActiveEditLoop.VerificationStatus != "skipped" {
		t.Fatalf("declined verification command must be skipped, got %#v", session.ActiveEditLoop)
	}
	if !containsStringMatching(session.ActiveEditLoop.RemainingRisks, "skipped") {
		t.Fatalf("expected skipped verification risk, got %#v", session.ActiveEditLoop.RemainingRisks)
	}
}

func TestEditLoopPassedVerificationClearsPriorVerificationRisk(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}
	session.RecordEditLoopEvent("fix file", EditLoopEvent{
		Kind:         "apply",
		Source:       "main",
		ToolName:     "write_file",
		Summary:      "wrote main.go",
		Status:       "ok",
		ChangedPaths: []string{"main.go"},
	})

	agent.recordEditLoopToolResult(ToolCall{Name: "run_shell"}, ToolExecutionResult{
		DisplayText: "go test ./... failed",
		Meta: map[string]any{
			"verification_like":   true,
			"verification_status": string(VerificationFailed),
			"command":             "go test ./...",
			"success":             false,
		},
	}, nil)
	agent.recordEditLoopToolResult(ToolCall{Name: "run_shell"}, ToolExecutionResult{
		DisplayText: "go test ./... passed",
		Meta: map[string]any{
			"verification_like":   true,
			"verification_status": string(VerificationPassed),
			"command":             "go test ./...",
			"success":             true,
		},
	}, nil)

	if session.ActiveEditLoop == nil {
		t.Fatalf("expected active edit loop")
	}
	if session.ActiveEditLoop.VerificationStatus != "passed" {
		t.Fatalf("expected passed verification status, got %#v", session.ActiveEditLoop)
	}
	if containsStringMatching(session.ActiveEditLoop.RemainingRisks, "verification") {
		t.Fatalf("passed verification should clear prior verification risks, got %#v", session.ActiveEditLoop.RemainingRisks)
	}

	agent.finalizeEditLoopOnReturn("Updated main.go. go test ./... passed.", false)
	if len(session.EditLoops) == 0 {
		t.Fatalf("expected finalized edit loop")
	}
	if session.EditLoops[0].Status != editLoopStatusCompleted {
		t.Fatalf("expected completed edit loop after successful verification, got %#v", session.EditLoops[0])
	}
}

func TestEditLoopPassedVerificationDoesNotClearDifferentFailureRisk(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}
	session.RecordEditLoopEvent("fix file", EditLoopEvent{
		Kind:         "apply",
		Source:       "main",
		ToolName:     "write_file",
		Summary:      "wrote main.go",
		Status:       "ok",
		ChangedPaths: []string{"main.go"},
	})

	agent.recordEditLoopToolResult(ToolCall{Name: "run_shell"}, ToolExecutionResult{
		DisplayText: "go test ./... failed",
		Meta: map[string]any{
			"verification_like":   true,
			"verification_status": string(VerificationFailed),
			"command":             "go test ./...",
			"success":             false,
		},
	}, nil)
	agent.recordEditLoopToolResult(ToolCall{Name: "run_shell"}, ToolExecutionResult{
		DisplayText: "go test ./cmd/kernforge passed",
		Meta: map[string]any{
			"verification_like":   true,
			"verification_status": string(VerificationPassed),
			"command":             "go test ./cmd/kernforge",
			"success":             true,
		},
	}, nil)

	if session.ActiveEditLoop == nil {
		t.Fatalf("expected active edit loop")
	}
	if !containsStringMatching(session.ActiveEditLoop.RemainingRisks, "go test ./...") {
		t.Fatalf("different passed command must not clear prior failure risk, got %#v", session.ActiveEditLoop.RemainingRisks)
	}

	agent.finalizeEditLoopOnReturn("Updated main.go. go test ./cmd/kernforge passed.", false)
	if len(session.EditLoops) == 0 {
		t.Fatalf("expected finalized edit loop")
	}
	if session.EditLoops[0].Status != editLoopStatusRiskAccepted {
		t.Fatalf("expected risk accepted while prior verification failure remains, got %#v", session.EditLoops[0])
	}
}

func TestEditLoopExplicitPassedVerificationFailsClosedOnConflict(t *testing.T) {
	cases := []struct {
		name string
		meta map[string]any
	}{
		{
			name: "success false",
			meta: map[string]any{
				"verification_status": string(VerificationPassed),
				"success":             false,
			},
		},
		{
			name: "failed bundle",
			meta: map[string]any{
				"verification_status": string(VerificationPassed),
				"bundle_status":       "failed",
			},
		},
		{
			name: "failed job",
			meta: map[string]any{
				"verification_status": string(VerificationPassed),
				"job_status":          "failed",
			},
		},
		{
			name: "failed command",
			meta: map[string]any{
				"verification_status":      string(VerificationPassed),
				"command_execution_status": "failed",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := editLoopToolEventStatus(ToolCall{Name: "run_shell"}, tc.meta, nil, true); got != "failed" {
				t.Fatalf("expected conflicting passed verification metadata to fail closed, got %q", got)
			}
		})
	}
}

func TestEditLoopRecordsBlockedWorkspaceShellWriteAsFailedApply(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}

	agent.recordEditLoopToolResult(ToolCall{Name: "run_shell"}, ToolExecutionResult{
		DisplayText: "manual shell write blocked",
		Meta: map[string]any{
			"command":        "Set-Content main.go 'oops'",
			"mutation_class": string(shellMutationWorkspaceWrite),
			"effect":         "execute",
			"success":        false,
		},
	}, errors.New("run_shell cannot perform manual workspace file writes"))

	if session.ActiveEditLoop == nil {
		t.Fatalf("expected blocked workspace shell write to create an edit loop")
	}
	if len(session.ActiveEditLoop.WorkerEvidence) != 1 || session.ActiveEditLoop.WorkerEvidence[0].Status != "error" {
		t.Fatalf("expected failed shell write to be recorded as worker evidence, got %#v", session.ActiveEditLoop)
	}
	agent.finalizeEditLoopOnReturn("All done.", false)
	if len(session.EditLoops) == 0 {
		t.Fatalf("expected finalized edit loop")
	}
	loop := session.EditLoops[0]
	if loop.Status != editLoopStatusRiskAccepted {
		t.Fatalf("expected blocked shell write to leave risk-accepted status, got %#v", loop)
	}
	if len(loop.RemainingRisks) == 0 {
		t.Fatalf("expected blocked shell write risk to remain, got %#v", loop)
	}
}

func TestEditLoopRetryDecisionEscalatesRepeatedFailure(t *testing.T) {
	session := NewSession("C:\\workspace", "scripted", "model", "", "default")
	for i := 0; i < 3; i++ {
		session.RecordEditLoopEvent("fix repeated failure", EditLoopEvent{
			Kind:   "retry",
			Source: "controller",
			Status: "planned",
			Detail: "go test ./... failed: TestParser rejects expected value",
		})
	}
	if session.ActiveEditLoop == nil {
		t.Fatalf("expected active edit loop")
	}
	loop := session.ActiveEditLoop
	if len(loop.RetryDecisions) != 3 {
		t.Fatalf("expected retry decisions, got %#v", loop.RetryDecisions)
	}
	last := loop.RetryDecisions[len(loop.RetryDecisions)-1]
	if last.Action != "escalate_reviewer" || last.SameFailureCount != 3 {
		t.Fatalf("expected repeated failure to escalate, got %#v", last)
	}
}

func TestEditLoopRecordsBackgroundVerificationBundleEvidence(t *testing.T) {
	session := NewSession("C:\\workspace", "scripted", "model", "", "default")
	agent := &Agent{Session: session}
	agent.recordEditLoopToolResult(ToolCall{Name: "run_shell_bundle_background"}, ToolExecutionResult{
		DisplayText: "bundle: bundle-1\nbundle_status: running",
		Meta: map[string]any{
			"verification_like":        true,
			"bundle_id":                "bundle-1",
			"bundle_status":            "running",
			"bundle_job_ids":           []string{"job-1"},
			"bundle_command_summaries": []string{"go test ./..."},
			"job_entries": []map[string]any{{
				"id":              "job-1",
				"status":          "running",
				"command_summary": "go test ./...",
				"log_path":        "logs/job-1.log",
			}},
		},
	}, nil)
	if session.ActiveEditLoop == nil {
		t.Fatalf("expected active edit loop")
	}
	loop := session.ActiveEditLoop
	if loop.VerificationStatus != "running" || loop.VerificationBundleID != "bundle-1" {
		t.Fatalf("expected running bundle verification evidence, got %#v", loop)
	}
	if len(loop.VerificationEvidence) != 1 || !strings.Contains(strings.Join(loop.VerificationEvidence[0].LogPaths, ","), "job-1.log") {
		t.Fatalf("expected verification evidence with log path, got %#v", loop.VerificationEvidence)
	}

	agent.recordEditLoopToolResult(ToolCall{Name: "check_shell_bundle"}, ToolExecutionResult{
		DisplayText: "bundle: bundle-1\nbundle_status: completed\nsummary: completed=1 running=0 failed=0 total=1",
		Meta: map[string]any{
			"verification_like": true,
			"bundle_id":         "bundle-1",
			"bundle_status":     "completed",
			"bundle_job_ids":    []string{"job-1"},
			"completed":         1,
			"running":           0,
			"failed":            0,
			"job_entries": []map[string]any{{
				"id":              "job-1",
				"status":          "completed",
				"command_summary": "go test ./...",
				"log_path":        "logs/job-1.log",
			}},
		},
	}, nil)
	if loop.VerificationStatus != "passed" {
		t.Fatalf("expected completed bundle to pass verification, got %#v", loop)
	}
	if len(loop.VerificationEvidence) != 1 {
		t.Fatalf("expected bundle evidence to be updated in place, got %#v", loop.VerificationEvidence)
	}
}

func TestEditLoopLogPathsFromMetaSupportsScalarAndListContracts(t *testing.T) {
	scalar := editLoopLogPathsFromMeta(map[string]any{
		"job_status": "completed",
		"log_path":   "logs/single.log",
	})
	if len(scalar) != 1 || scalar[0] != "logs/single.log" {
		t.Fatalf("expected scalar job_status to use top-level log_path only, got %#v", scalar)
	}

	list := editLoopLogPathsFromMeta(map[string]any{
		"job_status": "completed",
		"job_entries": []map[string]any{{
			"status":   "completed",
			"log_path": "logs/job-1.log",
		}},
	})
	if len(list) != 1 || list[0] != "logs/job-1.log" {
		t.Fatalf("expected job_entries list log path, got %#v", list)
	}

	legacy := editLoopLogPathsFromMeta(map[string]any{
		"job_status": []any{
			map[string]any{"status": "completed", "log_path": "logs/legacy.log"},
		},
	})
	if len(legacy) != 1 || legacy[0] != "logs/legacy.log" {
		t.Fatalf("expected legacy job_status list log path, got %#v", legacy)
	}
}

func TestEditLoopRecordsWorkerPatchTransactionEvidence(t *testing.T) {
	session := NewSession("C:\\workspace", "scripted", "model", "", "default")
	agent := &Agent{Session: session}
	agent.recordEditLoopToolResult(ToolCall{Name: "apply_patch"}, ToolExecutionResult{
		DisplayText: "updated src/parser.go",
		Meta: map[string]any{
			"effect":                     "edit",
			"parallel_worker":            true,
			"parallel_edit_worker":       true,
			"owner_node_id":              "plan-02",
			"changed_paths":              []string{"src/parser.go"},
			"owner_lease_paths":          []string{"src/parser.go"},
			"patch_transaction_id":       "patch-tx-1",
			"patch_transaction_entry_id": "patch-entry-1",
			"success":                    true,
		},
	}, nil)

	if session.ActiveEditLoop == nil {
		t.Fatalf("expected active edit loop")
	}
	loop := session.ActiveEditLoop
	if len(loop.WorkerEvidence) != 1 {
		t.Fatalf("expected worker evidence, got %#v", loop.WorkerEvidence)
	}
	worker := loop.WorkerEvidence[0]
	if worker.Source != "parallel-edit-worker" || worker.OwnerNodeID != "plan-02" {
		t.Fatalf("expected worker route evidence, got %#v", worker)
	}
	if worker.PatchTransactionID != "patch-tx-1" || worker.PatchTransactionEntryID != "patch-entry-1" {
		t.Fatalf("expected patch transaction evidence, got %#v", worker)
	}
	if !strings.Contains(strings.Join(worker.LeasePaths, ","), "src/parser.go") {
		t.Fatalf("expected lease path evidence, got %#v", worker)
	}
}

func TestEditLoopOutcomeContractCapturesFinalAnswerCoverage(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.RecordEditLoopEvent("fix file", EditLoopEvent{
		Kind:         "apply",
		Source:       "main",
		ToolName:     "write_file",
		Summary:      "wrote main.go",
		Status:       "ok",
		ChangedPaths: []string{"main.go"},
	})
	session.RecordEditLoopEvent("fix file", EditLoopEvent{
		Kind:     "verification",
		Source:   "auto",
		ToolName: "verify",
		Summary:  "go test ./... passed",
		Status:   "passed",
	})
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
		Store:     NewSessionStore(filepath.Join(root, "sessions")),
	}
	agent.finalizeEditLoopOnReturn("Updated main.go. go test ./... passed. No remaining blockers.", false)

	if len(session.EditLoops) == 0 || session.EditLoops[0].OutcomeContract == nil {
		t.Fatalf("expected finalized outcome contract, got %#v", session.EditLoops)
	}
	contract := session.EditLoops[0].OutcomeContract
	if !contract.AnswerMentionsVerification || !contract.AnswerMentionsRisk {
		t.Fatalf("expected final answer coverage flags, got %#v", contract)
	}
	if contract.WorkerEvidenceCount != 1 || contract.VerificationEvidenceCount != 1 {
		t.Fatalf("expected evidence counts in contract, got %#v", contract)
	}
}
