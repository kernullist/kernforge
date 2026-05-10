package main

import (
	"regexp"
	"strings"
)

type reviewRedactionPattern struct {
	Name string
	Re   *regexp.Regexp
}

var reviewRedactionPatterns = []reviewRedactionPattern{
	{Name: "openai_api_key", Re: regexp.MustCompile(`(?i)\bsk-[A-Za-z0-9_\-]{20,}\b`)},
	{Name: "github_token", Re: regexp.MustCompile(`(?i)\bgh[pousr]_[A-Za-z0-9_]{20,}\b`)},
	{Name: "private_key", Re: regexp.MustCompile(`(?is)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)},
	{Name: "bearer_token", Re: regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-]{20,}`)},
	{Name: "password_assignment", Re: regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|api[_-]?key)\s*[:=]\s*["']?[^"'\s]{8,}`)},
	{Name: "signed_url_secret", Re: regexp.MustCompile(`(?i)(sig|signature|token|access_token|X-Amz-Signature)=([A-Za-z0-9%._\-]+)`)},
}

func redactReviewRunEvidence(run *ReviewRun) ReviewRedactionReport {
	report := ReviewRedactionReport{Status: "clean"}
	if run == nil {
		return report
	}
	objective, objectiveReport := redactSensitiveText(run.Objective)
	run.Objective = objective
	run.Redaction = mergeReviewRedactionReports(run.Redaction, objectiveReport)
	request, requestReport := redactSensitiveText(run.RequestAnalysis.OriginalRequest)
	run.RequestAnalysis.OriginalRequest = request
	run.Redaction = mergeReviewRedactionReports(run.Redaction, requestReport)
	text, textReport := redactSensitiveText(run.Evidence.Text)
	run.Evidence.Text = text
	run.Redaction = mergeReviewRedactionReports(run.Redaction, textReport)
	if run.ChangeSet.DiffExcerpt != "" {
		diff, diffReport := redactSensitiveText(run.ChangeSet.DiffExcerpt)
		run.ChangeSet.DiffExcerpt = diff
		run.Redaction = mergeReviewRedactionReports(run.Redaction, diffReport)
	}
	report = run.Redaction
	if len(report.Patterns) > 0 || len(report.Warnings) > 0 || report.Redacted {
		report.Status = "warning"
	} else {
		report.Status = "clean"
	}
	return report
}

func redactSensitiveText(text string) (string, ReviewRedactionReport) {
	report := ReviewRedactionReport{Status: "clean"}
	if strings.TrimSpace(text) == "" {
		return text, report
	}
	redacted := text
	for _, pattern := range reviewRedactionPatterns {
		if pattern.Re.MatchString(redacted) {
			report.Redacted = true
			report.Patterns = append(report.Patterns, pattern.Name)
			redacted = pattern.Re.ReplaceAllString(redacted, "[REDACTED:"+pattern.Name+"]")
		}
	}
	if report.Redacted {
		report.Status = "warning"
		report.Warnings = append(report.Warnings, "sensitive-looking evidence was redacted before review artifact storage")
		report.Patterns = analysisUniqueStrings(report.Patterns)
	}
	return redacted, report
}

func mergeReviewRedactionReports(left ReviewRedactionReport, right ReviewRedactionReport) ReviewRedactionReport {
	out := left
	if right.Redacted {
		out.Redacted = true
	}
	out.Patterns = analysisUniqueStrings(append(out.Patterns, right.Patterns...))
	out.SensitiveRefs = analysisUniqueStrings(append(out.SensitiveRefs, right.SensitiveRefs...))
	out.Warnings = analysisUniqueStrings(append(out.Warnings, right.Warnings...))
	if out.Redacted || len(out.Warnings) > 0 {
		out.Status = "warning"
	} else if strings.TrimSpace(out.Status) == "" {
		out.Status = "clean"
	}
	return out
}
