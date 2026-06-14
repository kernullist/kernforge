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

// placeholderSecretTokens are substrings that mark a value as an obvious
// non-secret placeholder/default, not a real credential. D-D: a Flask sample app
// commonly ships SECRET_KEY = "dev_secret_key_change_me" or similar; redacting
// these created a noise finding every review and hid harmless code from the
// reviewer. Matching is case-insensitive and substring-based so variants like
// "changeme", "your-secret-here", "REPLACE_ME" are all treated as placeholders.
var placeholderSecretTokens = []string{
	"change_me", "changeme", "change-me", "replace_me", "replaceme", "replace-me",
	"your_", "your-", "yoursecret", "yourtoken", "yourkey", "placeholder",
	"example", "sample", "dummy", "fake", "todo", "xxxx", "<", ">", "{{", "}}",
	"dev_secret", "dev-secret", "devsecret", "test_secret", "default_secret",
	"insert_", "put_your", "change_this", "changethis",
}

// valueLooksLikePlaceholder reports whether the value is a self-describing
// placeholder/default rather than a real secret. Such values must not be
// redacted (no secret is leaked, and redaction only adds noise).
func valueLooksLikePlaceholder(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, token := range placeholderSecretTokens {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

// codeExpressionMarkers identify a value that is a code expression reading or
// computing something (a function call, a subscript, an attribute access, a
// concatenation) rather than a literal secret. D-D: lines like
// password = request.form["password"], token = generate_token(), or
// secret = hashlib.sha256(...).hexdigest() are normal code, not credentials, and
// must not be redacted (doing so mangles source the reviewer needs to see).
var codeExpressionMarkers = []string{
	"(", ")", "[", "]", "request.", "self.", "os.environ", "os.getenv",
	"getenv", "config[", "config.", "form.", "form[", "args.", "args[",
	"json.", "json[", "hashlib", "hmac", "secrets.", "uuid", "generate",
	"hash(", "+ ", " +", "%s", "{}", "f\"", "f'",
}

// valueLooksLikeCodeExpression reports whether the value is a code expression
// (function call / subscript / attribute access / format string) rather than a
// literal secret token.
func valueLooksLikeCodeExpression(value string) bool {
	v := strings.TrimSpace(value)
	for _, marker := range codeExpressionMarkers {
		if strings.Contains(v, marker) {
			return true
		}
	}
	return false
}

// valueLooksLikeCredential reports whether a captured value is plausibly a real
// secret rather than an ordinary word/identifier, a placeholder default, or a
// code expression. D-D: the previous U13/T13 rule still false-fired on Flask
// sample apps (placeholder SECRET_KEY defaults, password form-field reads, etc.).
// The tightened rule redacts only a long, opaque, high-entropy literal that is
// NOT a recognizable placeholder and NOT a code expression. When unsure we
// prefer NOT redacting, since a false redaction creates a noise finding and hides
// real code from the reviewer.
func valueLooksLikeCredential(value string) bool {
	v := strings.TrimSpace(value)
	if len(v) < 16 {
		// A genuine opaque secret is long. Raising the floor from 12 to 16 drops
		// short identifiers and most placeholders while keeping real API tokens.
		return false
	}
	// Self-describing placeholders and code expressions are never redacted.
	if valueLooksLikePlaceholder(v) {
		return false
	}
	if valueLooksLikeCodeExpression(v) {
		return false
	}
	hasDigit := false
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
		}
	}
	// Require a real entropy signature: an opaque secret mixes letters with
	// digits, or is mixed-case AND fairly long. A snake_case English phrase
	// (only lowercase + underscores, e.g. "user_password_field") looks like a
	// symbol-bearing token under the old rule but is not a secret, so underscores
	// or dashes alone no longer qualify; we require a digit, a non-word symbol, or
	// a mixed-case run together with sufficient length.
	hasNonWordSymbol := false
	for _, r := range v {
		switch r {
		case '_', '-':
			// snake/kebab separators are common in identifiers, not entropy
		default:
			if !(r >= '0' && r <= '9') && !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') {
				hasNonWordSymbol = true
			}
		}
	}
	if hasDigit || hasNonWordSymbol {
		return true
	}
	// Pure letters: require a mixed-case opaque-looking run of real length.
	return hasUpper && hasLower && len(v) >= 20
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
