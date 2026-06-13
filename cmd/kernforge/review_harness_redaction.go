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
	// password_assignment matches a credential-like KEY followed by a value that
	// actually looks like a secret. It is intentionally narrow so harmless string
	// literals such as Token: "security" are NOT mangled into invalid source.
	// The value must be a quoted/unquoted token of length >= 12 that mixes letter
	// classes (e.g. contains a digit or a non-word symbol), which a plain English
	// identifier does not. The credential-likeness is enforced in
	// redactPasswordAssignments, not by this anchor regex alone.
	{Name: "signed_url_secret", Re: regexp.MustCompile(`(?i)(sig|signature|token|access_token|X-Amz-Signature)=([A-Za-z0-9%._\-]+)`)},
}

// reviewPasswordAssignmentRe captures (1) the credential key, (2) the separator
// run, and (3) the candidate value (with optional surrounding quotes). The value
// is validated by valueLooksLikeCredential before any redaction is applied.
var reviewPasswordAssignmentRe = regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|client[_-]?secret)(\s*[:=]\s*)(["']?)([^"'\s]{6,})(["']?)`)

// valueLooksLikeCredential reports whether a captured value is plausibly a real
// secret rather than an ordinary word/identifier. A real credential is long and
// mixes character classes (digit and/or symbol), while a plain dictionary word
// or short identifier is not redacted.
func valueLooksLikeCredential(value string) bool {
	v := strings.TrimSpace(value)
	if len(v) < 12 {
		return false
	}
	hasDigit := false
	hasSymbol := false
	hasUpper := false
	hasLower := false
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= 'a' && r <= 'z':
			hasLower = true
		default:
			hasSymbol = true
		}
	}
	// Require entropy markers: either a digit, a symbol (-, _, +, /, =, .), or a
	// mixed-case run. A lowercase-only English word (e.g. "configuration") stays
	// untouched.
	if hasDigit || hasSymbol {
		return true
	}
	return hasUpper && hasLower
}

// redactPasswordAssignments replaces only credential-key assignments whose value
// looks like a real secret. Quotes are preserved so redacted source stays
// syntactically plausible (key: "[REDACTED:password_assignment]").
func redactPasswordAssignments(text string) (string, bool) {
	redacted := false
	out := reviewPasswordAssignmentRe.ReplaceAllStringFunc(text, func(match string) string {
		groups := reviewPasswordAssignmentRe.FindStringSubmatch(match)
		if len(groups) != 6 {
			return match
		}
		key, sep, openQuote, value, closeQuote := groups[1], groups[2], groups[3], groups[4], groups[5]
		if !valueLooksLikeCredential(value) {
			return match
		}
		redacted = true
		return key + sep + openQuote + "[REDACTED:password_assignment]" + closeQuote
	})
	return out, redacted
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
	original, originalReport := redactSensitiveText(run.OriginalMainProposal)
	run.OriginalMainProposal = original
	run.Redaction = mergeReviewRedactionReports(run.Redaction, originalReport)
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
	if updated, didRedact := redactPasswordAssignments(redacted); didRedact {
		report.Redacted = true
		report.Patterns = append(report.Patterns, "password_assignment")
		redacted = updated
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
