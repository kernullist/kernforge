package main

import (
	"fmt"
	"os"
	"strings"
)

// GitHub Actions CI adapter (rv-3-prbot Path 1).
//
// This file provides the pure parsing and scoping seam that lets the existing
// in-repo PR review run inside a GitHub Actions workflow. It deliberately holds
// no gh/network calls so it stays hermetically testable; the controlled
// comment-posting gate continues to live in handlePRReviewAutomationCommand and
// the honesty guard there (only an explicit /review pr may post). The adapter
// only derives the PR scope (base = BaseSHA) and reports a CI status line.

// PREventPayload captures the minimal PR identity that GitHub Actions exposes
// through GITHUB_* environment variables. It is the CI-side analogue of the
// gh pr view context the local automation already collects.
type PREventPayload struct {
	Owner    string
	Repo     string
	PRNumber string
	HeadSHA  string
	BaseSHA  string
}

// githubActionsEnv is the env-name set this adapter reads. Keeping the names in
// one place documents the contract the workflow file must satisfy.
const (
	githubActionsEnvFlag       = "GITHUB_ACTIONS"
	githubActionsEnvRepository = "GITHUB_REPOSITORY"
	githubActionsEnvRefName    = "GITHUB_REF_NAME"
	githubActionsEnvBaseRef    = "GITHUB_BASE_REF"
	githubActionsEnvHeadRef    = "GITHUB_HEAD_REF"
	githubActionsEnvSHA        = "GITHUB_SHA"
	githubActionsEnvOutput     = "GITHUB_OUTPUT"

	// The workflow exports the resolved PR identity through these explicit names
	// so the adapter does not have to parse the event payload JSON. They are set
	// from the github.event context in pr-review.yml.
	githubActionsEnvPRNumber   = "KERNFORGE_PR_NUMBER"
	githubActionsEnvPRHeadSHA  = "KERNFORGE_PR_HEAD_SHA"
	githubActionsEnvPRBaseSHA  = "KERNFORGE_PR_BASE_SHA"
	githubActionsEnvPROwner    = "KERNFORGE_PR_OWNER"
	githubActionsEnvPRRepoName = "KERNFORGE_PR_REPO"
)

// detectGitHubActionsContext reports whether the process is running inside a
// GitHub Actions runner. GitHub sets GITHUB_ACTIONS=true for every workflow
// step, so this is the canonical CI-context probe.
func detectGitHubActionsContext(getenv func(string) string) bool {
	if getenv == nil {
		getenv = os.Getenv
	}
	return strings.EqualFold(strings.TrimSpace(getenv(githubActionsEnvFlag)), "true")
}

// parsePREventPayloadFromEnv builds a PREventPayload from GITHUB_* variables.
// Owner/Repo are taken first from the explicit KERNFORGE_PR_OWNER/REPO names and
// otherwise split from GITHUB_REPOSITORY ("owner/repo"). The PR number and the
// head/base SHAs come from the explicit KERNFORGE_PR_* names the workflow sets;
// BaseSHA falls back to nothing when the event is not a PR so callers can detect
// the non-PR case. This function performs no I/O so it is fully testable.
func parsePREventPayloadFromEnv(getenv func(string) string) (PREventPayload, bool) {
	if getenv == nil {
		getenv = os.Getenv
	}
	payload := PREventPayload{}
	owner := strings.TrimSpace(getenv(githubActionsEnvPROwner))
	repo := strings.TrimSpace(getenv(githubActionsEnvPRRepoName))
	if owner == "" || repo == "" {
		if splitOwner, splitRepo, ok := splitGitHubRepository(getenv(githubActionsEnvRepository)); ok {
			if owner == "" {
				owner = splitOwner
			}
			if repo == "" {
				repo = splitRepo
			}
		}
	}
	payload.Owner = owner
	payload.Repo = repo
	payload.PRNumber = strings.TrimSpace(getenv(githubActionsEnvPRNumber))
	payload.HeadSHA = strings.TrimSpace(getenv(githubActionsEnvPRHeadSHA))
	payload.BaseSHA = strings.TrimSpace(getenv(githubActionsEnvPRBaseSHA))
	if payload.HeadSHA == "" {
		// GITHUB_SHA is the checked-out commit; on a pull_request event this is the
		// merge commit, which is still a valid head anchor for diffing.
		payload.HeadSHA = strings.TrimSpace(getenv(githubActionsEnvSHA))
	}
	valid := payload.PRNumber != "" || payload.BaseSHA != ""
	return payload, valid
}

// splitGitHubRepository splits the GITHUB_REPOSITORY "owner/repo" form. It
// returns ok=false when the value is empty or malformed.
func splitGitHubRepository(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false
	}
	parts := strings.SplitN(value, "/", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	owner := strings.TrimSpace(parts[0])
	repo := strings.TrimSpace(parts[1])
	if owner == "" || repo == "" {
		return "", "", false
	}
	return owner, repo, true
}

// applyPREventScopeToReviewOptions scopes a review run to the PR diff by setting
// BaseRef to the PR base commit. It mirrors how a local "/review pr --base <sha>"
// invocation scopes evidence to a merge-base three-dot diff. An explicit Commit
// or BaseRef already set by the caller is preserved so a user override wins.
func applyPREventScopeToReviewOptions(payload PREventPayload, opts ReviewHarnessOptions) ReviewHarnessOptions {
	if strings.TrimSpace(opts.Commit) != "" {
		return opts
	}
	if strings.TrimSpace(opts.BaseRef) != "" {
		return opts
	}
	base := strings.TrimSpace(payload.BaseSHA)
	if base == "" {
		return opts
	}
	opts.Target = reviewTargetPR
	opts.BaseRef = base
	return opts
}

// reviewArgsRequestGitHubActions reports whether the /review args opt into the
// CI path. It matches the same flag tokens parsePRReviewAutomationOptions
// recognizes so the scoping decision and the posting gate stay aligned.
func reviewArgsRequestGitHubActions(args string) bool {
	lower := strings.ToLower(args)
	return strings.Contains(lower, "--github-actions") || strings.Contains(lower, "--ci")
}

// applyGitHubActionsReviewScope scopes a /review pr run to the PR diff when the
// args opt into the CI path AND the process is genuinely running inside a
// GitHub Actions runner. Requiring both prevents a stray --ci token from
// altering local review scope. An explicit --base/--commit still wins.
func applyGitHubActionsReviewScope(args string, opts ReviewHarnessOptions, getenv func(string) string) ReviewHarnessOptions {
	if !reviewArgsRequestGitHubActions(args) {
		return opts
	}
	if !detectGitHubActionsContext(getenv) {
		return opts
	}
	payload, ok := parsePREventPayloadFromEnv(getenv)
	if !ok {
		return opts
	}
	return applyPREventScopeToReviewOptions(payload, opts)
}

// githubActionsStatusLine renders the single status line the workflow surfaces
// for an observer (and the GITHUB_OUTPUT "status" key). It is plain ASCII.
func githubActionsStatusLine(payload PREventPayload, verdict string, postStatus string) string {
	pr := strings.TrimSpace(payload.PRNumber)
	if pr == "" {
		pr = "unknown"
	}
	verdict = strings.TrimSpace(verdict)
	if verdict == "" {
		verdict = "unknown"
	}
	postStatus = strings.TrimSpace(postStatus)
	if postStatus == "" {
		postStatus = "not_requested"
	}
	return fmt.Sprintf("pr=%s verdict=%s comment_post=%s", pr, verdict, postStatus)
}

// writeGitHubActionsOutput appends a "key=value" line to the file named by
// GITHUB_OUTPUT, which is how a workflow step exposes outputs to later steps.
// When GITHUB_OUTPUT is unset (for example outside CI) it is a no-op so the same
// code path is safe to run locally. Values are sanitized to a single line so a
// stray newline cannot inject extra outputs.
func writeGitHubActionsOutput(getenv func(string) string, key string, value string) error {
	if getenv == nil {
		getenv = os.Getenv
	}
	path := strings.TrimSpace(getenv(githubActionsEnvOutput))
	if path == "" {
		return nil
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("github actions output key is empty")
	}
	value = sanitizeGitHubActionsOutputValue(value)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := fmt.Fprintf(file, "%s=%s\n", key, value); err != nil {
		return err
	}
	return nil
}

// sanitizeGitHubActionsOutputValue collapses any CR/LF in a value to spaces so a
// single output line cannot be split into multiple key=value entries.
func sanitizeGitHubActionsOutputValue(value string) string {
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.TrimSpace(value)
}
