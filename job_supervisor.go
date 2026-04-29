package main

import (
	"fmt"
	"strings"
	"time"
)

const jobSupervisorStaleAfter = 15 * time.Minute

type JobSupervisorReport struct {
	GeneratedAt     time.Time              `json:"generated_at,omitempty"`
	Total           int                    `json:"total,omitempty"`
	Running         int                    `json:"running,omitempty"`
	Completed       int                    `json:"completed,omitempty"`
	Failed          int                    `json:"failed,omitempty"`
	Canceled        int                    `json:"canceled,omitempty"`
	Stale           int                    `json:"stale,omitempty"`
	BundleTotal     int                    `json:"bundle_total,omitempty"`
	BundleRunning   int                    `json:"bundle_running,omitempty"`
	BundleCompleted int                    `json:"bundle_completed,omitempty"`
	BundleFailed    int                    `json:"bundle_failed,omitempty"`
	BundleCanceled  int                    `json:"bundle_canceled,omitempty"`
	BundleStale     int                    `json:"bundle_stale,omitempty"`
	Summaries       []string               `json:"summaries,omitempty"`
	BundleSummaries []string               `json:"bundle_summaries,omitempty"`
	Findings        []CodingHarnessFinding `json:"findings,omitempty"`
}

func (a *Agent) buildJobSupervisorReport(reply string) JobSupervisorReport {
	report := JobSupervisorReport{GeneratedAt: time.Now()}
	if a == nil || a.Session == nil {
		return report
	}
	a.refreshBackgroundJobs()
	now := time.Now()
	for _, job := range a.Session.BackgroundJobs {
		job.Normalize()
		if strings.TrimSpace(job.ID) == "" {
			continue
		}
		report.Total++
		status := strings.TrimSpace(strings.ToLower(job.Status))
		switch status {
		case "completed":
			report.Completed++
		case "failed":
			report.Failed++
			severity := "blocker"
			detail := "A background job failed. The final answer should report the failed job or repair the failure before concluding."
			if replyMentionsBackgroundFailure(reply) || replyMentionsVerificationBlocker(reply) {
				severity = "warning"
				detail = "A background job failed, and the final answer appears to acknowledge a failure or blocker."
			}
			report.Findings = append(report.Findings, CodingHarnessFinding{
				Severity: severity,
				Title:    "Background job failed",
				Detail:   detail + " " + jobSupervisorJobSummary(job),
			})
		case "canceled", "preempted", "superseded":
			report.Canceled++
		case "stale":
			report.Stale++
		default:
			report.Running++
			if !job.UpdatedAt.IsZero() && now.Sub(job.UpdatedAt) > jobSupervisorStaleAfter {
				report.Stale++
				severity := "blocker"
				detail := "A running background job has not reported progress recently. Poll, cancel, or explicitly report that stale state before concluding."
				if replyMentionsBackgroundStillRunning(reply) || replyMentionsBackgroundStale(reply) {
					severity = "warning"
					detail = "A background job may be stale, and the final answer appears to acknowledge unfinished background work."
				}
				report.Findings = append(report.Findings, CodingHarnessFinding{
					Severity: severity,
					Title:    "Background job may be stale",
					Detail:   detail + " " + jobSupervisorJobSummary(job),
				})
			}
		}
		report.Summaries = append(report.Summaries, jobSupervisorJobSummary(job))
	}
	for _, bundle := range a.Session.BackgroundBundles {
		bundle.Normalize()
		if strings.TrimSpace(bundle.ID) == "" {
			continue
		}
		status := strings.TrimSpace(strings.ToLower(bundle.Status))
		report.BundleTotal++
		switch status {
		case "completed":
			report.BundleCompleted++
		case "failed":
			report.BundleFailed++
			severity := "blocker"
			detail := "A background bundle failed. The final answer should report the failed bundle or repair the failure before concluding."
			if replyMentionsBackgroundFailure(reply) || replyMentionsVerificationBlocker(reply) {
				severity = "warning"
				detail = "A background bundle failed, and the final answer appears to acknowledge a failure or blocker."
			}
			report.Findings = append(report.Findings, CodingHarnessFinding{
				Severity: severity,
				Title:    "Background bundle failed",
				Detail:   detail + " " + jobSupervisorBundleSummary(bundle),
			})
		case "canceled", "preempted", "superseded":
			report.BundleCanceled++
		case "stale":
			report.BundleStale++
		default:
			report.BundleRunning++
			if len(bundle.JobIDs) == 0 && !replyMentionsBackgroundStillRunning(reply) {
				report.Findings = append(report.Findings, CodingHarnessFinding{
					Severity: "blocker",
					Title:    "Running background bundle needs acknowledgement",
					Detail:   "A background bundle is still running but has no synchronized job details. Poll or acknowledge it before concluding. " + jobSupervisorBundleSummary(bundle),
				})
			}
		}
		report.BundleSummaries = append(report.BundleSummaries, jobSupervisorBundleSummary(bundle))
	}
	if report.Running > 0 && !replyMentionsBackgroundStillRunning(reply) {
		report.Findings = append(report.Findings, CodingHarnessFinding{
			Severity: "blocker",
			Title:    "Running background work needs acknowledgement",
			Detail:   fmt.Sprintf("%d background job(s) are still running; the final answer should mention that or poll/cancel them.", report.Running),
		})
	}
	report.Normalize()
	return report
}

func jobSupervisorJobSummary(job BackgroundShellJob) string {
	job.Normalize()
	parts := []string{}
	if job.ID != "" {
		parts = append(parts, job.ID)
	}
	if job.Status != "" {
		parts = append(parts, "["+job.Status+"]")
	}
	if job.CommandSummary != "" {
		parts = append(parts, compactPromptSection(job.CommandSummary, 120))
	} else if job.Command != "" {
		parts = append(parts, compactPromptSection(job.Command, 120))
	}
	if job.LastOutput != "" {
		parts = append(parts, "last="+compactPromptSection(firstNonEmptyLine(job.LastOutput), 100))
	}
	return strings.Join(parts, " ")
}

func jobSupervisorBundleSummary(bundle BackgroundShellBundle) string {
	bundle.Normalize()
	parts := []string{}
	if bundle.ID != "" {
		parts = append(parts, "bundle="+bundle.ID)
	}
	if bundle.Status != "" {
		parts = append(parts, "["+bundle.Status+"]")
	}
	if bundle.LastSummary != "" {
		parts = append(parts, compactPromptSection(bundle.LastSummary, 140))
	} else if bundle.Summary != "" {
		parts = append(parts, compactPromptSection(bundle.Summary, 140))
	} else if len(bundle.CommandSummaries) > 0 {
		parts = append(parts, compactPromptSection(strings.Join(bundle.CommandSummaries, " | "), 140))
	}
	return strings.Join(parts, " ")
}

func (r *JobSupervisorReport) Normalize() {
	if r == nil {
		return
	}
	r.Summaries = normalizeTaskStateList(r.Summaries, 12)
	r.BundleSummaries = normalizeTaskStateList(r.BundleSummaries, 12)
	r.Findings = normalizeCodingHarnessFindings(r.Findings)
}

func (r JobSupervisorReport) RenderPromptSection() string {
	r.Normalize()
	lines := make([]string, 0, 8)
	if r.Total > 0 {
		lines = append(lines, fmt.Sprintf("- Jobs: total=%d running=%d completed=%d failed=%d canceled=%d stale=%d", r.Total, r.Running, r.Completed, r.Failed, r.Canceled, r.Stale))
	}
	if len(r.Summaries) > 0 {
		lines = append(lines, "- Summaries: "+strings.Join(r.Summaries, " | "))
	}
	if r.BundleTotal > 0 {
		lines = append(lines, fmt.Sprintf("- Bundles: total=%d running=%d completed=%d failed=%d canceled=%d stale=%d", r.BundleTotal, r.BundleRunning, r.BundleCompleted, r.BundleFailed, r.BundleCanceled, r.BundleStale))
	}
	if len(r.BundleSummaries) > 0 {
		lines = append(lines, "- Bundle summaries: "+strings.Join(r.BundleSummaries, " | "))
	}
	for _, finding := range r.Findings {
		lines = append(lines, fmt.Sprintf("- [%s] %s: %s", finding.Severity, finding.Title, compactPromptSection(finding.Detail, 220)))
	}
	return strings.Join(lines, "\n")
}

func replyMentionsBackgroundFailure(reply string) bool {
	lower := strings.ToLower(strings.TrimSpace(reply))
	if lower == "" {
		return false
	}
	return containsAny(lower,
		"background", "job", "bundle", "verification", "test", "build",
		"백그라운드", "작업", "검증", "테스트", "빌드",
	) && containsAny(lower,
		"failed", "failure", "failing", "error", "blocker", "blocked",
		"실패", "오류", "에러", "블로커", "막혀",
	)
}

func replyMentionsBackgroundStale(reply string) bool {
	lower := strings.ToLower(strings.TrimSpace(reply))
	if lower == "" {
		return false
	}
	return containsAny(lower, "stale", "no progress", "hung", "stuck", "멈춰", "응답 없", "진행 없") &&
		containsAny(lower, "background", "job", "bundle", "백그라운드", "작업")
}
