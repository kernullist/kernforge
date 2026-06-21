package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func githubActionsTestGetenv(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}

func TestParsePREventPayloadFromEnv(t *testing.T) {
	getenv := githubActionsTestGetenv(map[string]string{
		githubActionsEnvRepository: "kernullist/kernforge",
		githubActionsEnvPRNumber:   "42",
		githubActionsEnvPRHeadSHA:  "headsha111",
		githubActionsEnvPRBaseSHA:  "basesha000",
	})
	payload, ok := parsePREventPayloadFromEnv(getenv)
	if !ok {
		t.Fatalf("expected a valid PR payload")
	}
	if payload.Owner != "kernullist" || payload.Repo != "kernforge" {
		t.Fatalf("expected owner/repo split from GITHUB_REPOSITORY, got %#v", payload)
	}
	if payload.PRNumber != "42" {
		t.Fatalf("expected PR number 42, got %q", payload.PRNumber)
	}
	if payload.HeadSHA != "headsha111" || payload.BaseSHA != "basesha000" {
		t.Fatalf("expected head/base SHAs, got %#v", payload)
	}
}

func TestParsePREventPayloadPrefersExplicitOwnerRepo(t *testing.T) {
	getenv := githubActionsTestGetenv(map[string]string{
		githubActionsEnvRepository: "fallbackowner/fallbackrepo",
		githubActionsEnvPROwner:    "explicitowner",
		githubActionsEnvPRRepoName: "explicitrepo",
		githubActionsEnvPRBaseSHA:  "base123",
	})
	payload, ok := parsePREventPayloadFromEnv(getenv)
	if !ok {
		t.Fatalf("expected valid payload")
	}
	if payload.Owner != "explicitowner" || payload.Repo != "explicitrepo" {
		t.Fatalf("expected explicit owner/repo to win, got %#v", payload)
	}
}

func TestParsePREventPayloadHeadFallsBackToGitHubSHA(t *testing.T) {
	getenv := githubActionsTestGetenv(map[string]string{
		githubActionsEnvPRNumber:  "7",
		githubActionsEnvSHA:       "mergesha999",
		githubActionsEnvPRBaseSHA: "base777",
	})
	payload, ok := parsePREventPayloadFromEnv(getenv)
	if !ok {
		t.Fatalf("expected valid payload")
	}
	if payload.HeadSHA != "mergesha999" {
		t.Fatalf("expected head SHA to fall back to GITHUB_SHA, got %q", payload.HeadSHA)
	}
}

func TestParsePREventPayloadInvalidWhenNotPR(t *testing.T) {
	getenv := githubActionsTestGetenv(map[string]string{
		githubActionsEnvRepository: "kernullist/kernforge",
		githubActionsEnvSHA:        "push123",
	})
	if _, ok := parsePREventPayloadFromEnv(getenv); ok {
		t.Fatalf("expected non-PR event (no PR number and no base SHA) to be invalid")
	}
}

func TestDetectGitHubActionsContext(t *testing.T) {
	if !detectGitHubActionsContext(githubActionsTestGetenv(map[string]string{githubActionsEnvFlag: "true"})) {
		t.Fatalf("expected GITHUB_ACTIONS=true to be detected as CI")
	}
	if detectGitHubActionsContext(githubActionsTestGetenv(map[string]string{githubActionsEnvFlag: "false"})) {
		t.Fatalf("expected GITHUB_ACTIONS=false to be non-CI")
	}
	if detectGitHubActionsContext(githubActionsTestGetenv(map[string]string{})) {
		t.Fatalf("expected missing GITHUB_ACTIONS to be non-CI")
	}
}

func TestSplitGitHubRepository(t *testing.T) {
	owner, repo, ok := splitGitHubRepository("kernullist/kernforge")
	if !ok || owner != "kernullist" || repo != "kernforge" {
		t.Fatalf("expected clean split, got owner=%q repo=%q ok=%t", owner, repo, ok)
	}
	if _, _, ok := splitGitHubRepository("noslash"); ok {
		t.Fatalf("expected malformed value to fail split")
	}
	if _, _, ok := splitGitHubRepository(""); ok {
		t.Fatalf("expected empty value to fail split")
	}
}

func TestApplyPREventScopeToReviewOptionsSetsBase(t *testing.T) {
	payload := PREventPayload{BaseSHA: "base-abc", PRNumber: "12"}
	opts := applyPREventScopeToReviewOptions(payload, ReviewHarnessOptions{Target: reviewTargetAuto})
	if opts.BaseRef != "base-abc" {
		t.Fatalf("expected base ref to be scoped to PR base SHA, got %q", opts.BaseRef)
	}
	if opts.Target != reviewTargetPR {
		t.Fatalf("expected target to be scoped to pr, got %q", opts.Target)
	}
}

func TestApplyPREventScopePreservesExplicitOverrides(t *testing.T) {
	payload := PREventPayload{BaseSHA: "base-abc"}

	withBase := applyPREventScopeToReviewOptions(payload, ReviewHarnessOptions{BaseRef: "user-base"})
	if withBase.BaseRef != "user-base" {
		t.Fatalf("expected explicit base ref to win, got %q", withBase.BaseRef)
	}

	withCommit := applyPREventScopeToReviewOptions(payload, ReviewHarnessOptions{Commit: "user-commit"})
	if withCommit.BaseRef != "" || withCommit.Commit != "user-commit" {
		t.Fatalf("expected explicit commit to win and base to stay empty, got %#v", withCommit)
	}
}

func TestApplyPREventScopeNoBaseIsNoop(t *testing.T) {
	opts := applyPREventScopeToReviewOptions(PREventPayload{PRNumber: "5"}, ReviewHarnessOptions{Target: reviewTargetAuto})
	if opts.BaseRef != "" || opts.Target != reviewTargetAuto {
		t.Fatalf("expected no scoping without a base SHA, got %#v", opts)
	}
}

func TestApplyGitHubActionsReviewScopeRequiresCIAndFlag(t *testing.T) {
	ciEnv := map[string]string{
		githubActionsEnvFlag:      "true",
		githubActionsEnvPRNumber:  "9",
		githubActionsEnvPRBaseSHA: "base-ci",
	}

	// Flag present + CI context: scope applies.
	scoped := applyGitHubActionsReviewScope("pr --github-actions --draft-comments", ReviewHarnessOptions{Target: reviewTargetPR}, githubActionsTestGetenv(ciEnv))
	if scoped.BaseRef != "base-ci" {
		t.Fatalf("expected CI scope to set base ref, got %q", scoped.BaseRef)
	}

	// Flag present but not in CI: no scoping (stray --ci must not alter local scope).
	localEnv := map[string]string{
		githubActionsEnvPRBaseSHA: "base-ci",
	}
	local := applyGitHubActionsReviewScope("pr --ci", ReviewHarnessOptions{Target: reviewTargetPR}, githubActionsTestGetenv(localEnv))
	if local.BaseRef != "" {
		t.Fatalf("expected no scoping outside CI, got %q", local.BaseRef)
	}

	// CI context but no opt-in flag: no scoping.
	noFlag := applyGitHubActionsReviewScope("pr --draft-comments", ReviewHarnessOptions{Target: reviewTargetPR}, githubActionsTestGetenv(ciEnv))
	if noFlag.BaseRef != "" {
		t.Fatalf("expected no scoping without the opt-in flag, got %q", noFlag.BaseRef)
	}
}

func TestWriteGitHubActionsOutputAppendsAndSanitizes(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "github_output")
	getenv := githubActionsTestGetenv(map[string]string{githubActionsEnvOutput: outPath})

	if err := writeGitHubActionsOutput(getenv, "status", "pr=42 verdict=approved"); err != nil {
		t.Fatalf("writeGitHubActionsOutput: %v", err)
	}
	if err := writeGitHubActionsOutput(getenv, "verdict", "line1\nline2\r\nline3"); err != nil {
		t.Fatalf("writeGitHubActionsOutput second: %v", err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "status=pr=42 verdict=approved\n") {
		t.Fatalf("expected appended status line, got %q", text)
	}
	if strings.Contains(text, "\nline2") || strings.Contains(text, "line2\n") {
		t.Fatalf("expected newlines in value to be collapsed, got %q", text)
	}
	if !strings.Contains(text, "verdict=line1 line2 line3\n") {
		t.Fatalf("expected sanitized single-line verdict, got %q", text)
	}
}

func TestWriteGitHubActionsOutputNoopWithoutEnv(t *testing.T) {
	if err := writeGitHubActionsOutput(githubActionsTestGetenv(map[string]string{}), "status", "value"); err != nil {
		t.Fatalf("expected no-op without GITHUB_OUTPUT, got %v", err)
	}
}

func TestWriteGitHubActionsOutputRejectsEmptyKey(t *testing.T) {
	dir := t.TempDir()
	getenv := githubActionsTestGetenv(map[string]string{githubActionsEnvOutput: filepath.Join(dir, "out")})
	if err := writeGitHubActionsOutput(getenv, "  ", "value"); err == nil {
		t.Fatalf("expected an error for an empty output key")
	}
}

func TestGitHubActionsStatusLine(t *testing.T) {
	line := githubActionsStatusLine(PREventPayload{PRNumber: "42"}, "approved", "posted")
	if line != "pr=42 verdict=approved comment_post=posted" {
		t.Fatalf("unexpected status line: %q", line)
	}
	fallback := githubActionsStatusLine(PREventPayload{}, "", "")
	if fallback != "pr=unknown verdict=unknown comment_post=not_requested" {
		t.Fatalf("unexpected fallback status line: %q", fallback)
	}
}
