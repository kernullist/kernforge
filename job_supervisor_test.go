package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestJobSupervisorReportSummarizesJobs(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	session := NewSession(root, "scripted", "model", "", "default")
	session.BackgroundJobs = []BackgroundShellJob{
		{
			ID:             "job-running",
			CommandSummary: "go test ./...",
			Status:         "running",
			StartedAt:      now.Add(-20 * time.Minute),
			UpdatedAt:      now.Add(-20 * time.Minute),
		},
		{
			ID:             "job-failed",
			CommandSummary: "go vet ./...",
			Status:         "failed",
			LastOutput:     "vet failed",
			StartedAt:      now.Add(-2 * time.Minute),
			UpdatedAt:      now.Add(-1 * time.Minute),
		},
	}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	report := agent.buildJobSupervisorReport("The background job is still running.")
	if report.Total != 2 || report.Running != 1 || report.Failed != 1 || report.Stale != 1 {
		t.Fatalf("unexpected job supervisor counters: %#v", report)
	}
	rendered := report.RenderPromptSection()
	if !strings.Contains(rendered, "job-running") || !strings.Contains(rendered, "job-failed") {
		t.Fatalf("expected job ids in rendered report, got %q", rendered)
	}
	if len(report.Findings) == 0 {
		t.Fatalf("expected findings for stale/failed jobs")
	}
}

func TestJobSupervisorBlocksWhenRunningWorkIsNotMentioned(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.BackgroundJobs = []BackgroundShellJob{{
		ID:             "job-1",
		CommandSummary: "go test ./...",
		Status:         "running",
		StartedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	report := agent.buildJobSupervisorReport("All done.")
	found := false
	for _, finding := range report.Findings {
		if strings.Contains(finding.Title, "Running background work") {
			found = true
			break
		}
	}
	if !found || !codingHarnessFindingsHaveBlockers(report.Findings) {
		t.Fatalf("expected running-work acknowledgement blocker, got %#v", report.Findings)
	}
}

func TestPreFinalHarnessBlocksUnmentionedFailedBackgroundJob(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	session := NewSession(root, "scripted", "model", "", "default")
	session.BackgroundJobs = []BackgroundShellJob{{
		ID:             "job-failed",
		CommandSummary: "go test ./...",
		Status:         "failed",
		LastOutput:     "FAIL ./...",
		StartedAt:      now.Add(-2 * time.Minute),
		UpdatedAt:      now.Add(-1 * time.Minute),
	}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	approved, feedback := agent.runPreFinalCodingHarnesses(context.Background(), "All done.", false, false)
	if approved {
		t.Fatalf("expected unmentioned failed background job to block final answer")
	}
	if !strings.Contains(feedback, "Background job failed") {
		t.Fatalf("expected failed job feedback, got %q", feedback)
	}

	approved, feedback = agent.runPreFinalCodingHarnesses(context.Background(), "Background verification failed; blocker remains.", false, false)
	if !approved {
		t.Fatalf("expected acknowledged failed background job to be non-blocking, feedback=%q", feedback)
	}
}

func TestJobSupervisorTracksFailedBundles(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	session := NewSession(root, "scripted", "model", "", "default")
	session.BackgroundBundles = []BackgroundShellBundle{{
		ID:               "bundle-1",
		Status:           "failed",
		LastSummary:      "completed=0 running=0 failed=1 canceled=0 total=1",
		StartedAt:        now.Add(-2 * time.Minute),
		UpdatedAt:        now.Add(-1 * time.Minute),
		JobIDs:           []string{"job-1"},
		VerificationLike: true,
	}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	report := agent.buildJobSupervisorReport("All done.")
	if report.BundleTotal != 1 || report.BundleFailed != 1 {
		t.Fatalf("unexpected bundle counters: %#v", report)
	}
	rendered := report.RenderPromptSection()
	if !strings.Contains(rendered, "bundle-1") || !strings.Contains(rendered, "Background bundle failed") {
		t.Fatalf("expected failed bundle in rendered report, got %q", rendered)
	}
	if !codingHarnessFindingsHaveBlockers(report.Findings) {
		t.Fatalf("expected unmentioned failed bundle to be blocking, got %#v", report.Findings)
	}
}

func TestPreFinalHarnessStoresJobSupervisorReport(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	session := NewSession(root, "scripted", "model", "", "default")
	session.BackgroundJobs = []BackgroundShellJob{{
		ID:             "job-1",
		CommandSummary: "go test ./...",
		Status:         "running",
		StartedAt:      now,
		UpdatedAt:      now,
	}}
	agent := &Agent{
		Session:   session,
		Workspace: Workspace{BaseRoot: root, Root: root},
	}

	approved, feedback := agent.runPreFinalCodingHarnesses(context.Background(), "The background job is still running.", false, false)
	if !approved {
		t.Fatalf("expected acknowledged running work to be non-blocking, feedback=%q", feedback)
	}
	if session.LastJobSupervisorReport == nil {
		t.Fatalf("expected pre-final harness to store job supervisor report")
	}
	if session.LastJobSupervisorReport.Total != 1 || session.LastJobSupervisorReport.Running != 1 {
		t.Fatalf("unexpected stored job supervisor report: %#v", session.LastJobSupervisorReport)
	}
	if session.LastCodingHarnessReport == nil || session.LastCodingHarnessReport.JobSupervisor.Total != 1 {
		t.Fatalf("expected coding harness report to include job supervisor data, got %#v", session.LastCodingHarnessReport)
	}
}

func TestFinalReviewerPromptIncludesJobSupervisorReport(t *testing.T) {
	root := t.TempDir()
	session := NewSession(root, "scripted", "model", "", "default")
	session.LastJobSupervisorReport = &JobSupervisorReport{
		Total:   1,
		Running: 1,
		Summaries: []string{
			"job-1 [running] go test ./...",
		},
	}

	prompt := buildInteractiveFinalAnswerReviewerPrompt(session, "done", false)
	if !strings.Contains(prompt, "Job supervisor report") {
		t.Fatalf("expected reviewer prompt to include job supervisor report, got %q", prompt)
	}
	if !strings.Contains(prompt, "job-1") {
		t.Fatalf("expected reviewer prompt to include job summary, got %q", prompt)
	}
}
