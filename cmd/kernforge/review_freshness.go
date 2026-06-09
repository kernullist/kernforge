package main

import (
	"fmt"
	"strings"
	"time"
)

var (
	reviewFreshnessGitBranchProvider       = delegationGitBranch
	reviewFreshnessChangedFilesProvider    = delegationChangedFiles
	reviewFreshnessGitStatusUsableProvider = reviewScopeGitStatusLooksUsable
)

func reviewLatestFreshnessForRoot(root string, run ReviewRun) ReviewFreshness {
	var currentChanged []string
	if strings.TrimSpace(root) != "" {
		currentChanged = reviewCurrentChangedPaths(root)
	}
	return reviewLatestFreshnessAgainstPaths(root, run, currentChanged)
}

func reviewLatestFreshnessAgainstPaths(root string, run ReviewRun, currentChanged []string) ReviewFreshness {
	freshness := run.Freshness
	if strings.TrimSpace(freshness.ReviewFingerprint) == "" {
		freshness.ReviewFingerprint = strings.TrimSpace(run.ReviewFingerprint)
	}
	freshness.CheckedAt = time.Now()
	var invalidated []string
	var reasons []string
	if strings.TrimSpace(run.ReviewFingerprint) != "" &&
		strings.TrimSpace(freshness.ReviewFingerprint) != "" &&
		!strings.EqualFold(strings.TrimSpace(run.ReviewFingerprint), strings.TrimSpace(freshness.ReviewFingerprint)) {
		invalidated = append(invalidated, "review_fingerprint")
		reasons = append(reasons, "review fingerprint changed")
	}
	if strings.TrimSpace(root) != "" {
		branchProvider := reviewFreshnessGitBranchProvider
		if branchProvider == nil {
			branchProvider = delegationGitBranch
		}
		if branch := branchProvider(root); strings.TrimSpace(branch) != "" && strings.TrimSpace(run.Branch) != "" && !strings.EqualFold(branch, run.Branch) {
			invalidated = append(invalidated, "branch")
			reasons = append(reasons, fmt.Sprintf("branch changed from %s to %s", run.Branch, branch))
		}
		if len(currentChanged) > 0 {
			if missing := reviewUnreviewedChangedPaths(run.ChangeSet.ChangedPaths, currentChanged); len(missing) > 0 {
				invalidated = append(invalidated, "changed_paths")
				reasons = append(reasons, "unreviewed changed files: "+strings.Join(limitStrings(missing, 6), ", "))
			}
		}
		if mismatches := reviewCurrentFileHashMismatches(root, run.ArtifactIntegrity); len(mismatches) > 0 {
			invalidated = append(invalidated, "file_hashes")
			reasons = append(reasons, "reviewed files changed since review: "+strings.Join(limitStrings(mismatches, 6), ", "))
		}
	}
	freshness.InvalidatedBy = analysisUniqueStrings(append(freshness.InvalidatedBy, invalidated...))
	if len(reasons) > 0 {
		freshness.Stale = true
		freshness.StaleReason = strings.Join(reasons, "; ")
	}
	return freshness
}

func reviewCurrentChangedPaths(root string) []string {
	usableProvider := reviewFreshnessGitStatusUsableProvider
	if usableProvider == nil {
		usableProvider = reviewScopeGitStatusLooksUsable
	}
	if !usableProvider(root) {
		return nil
	}
	changedProvider := reviewFreshnessChangedFilesProvider
	if changedProvider == nil {
		changedProvider = delegationChangedFiles
	}
	return normalizeCompletionAuditReviewPaths(filterReviewablePaths(changedProvider(root)))
}

func reviewUnreviewedChangedPaths(reviewed []string, currentChanged []string) []string {
	currentChanged = normalizeCompletionAuditReviewPaths(currentChanged)
	if len(currentChanged) == 0 {
		return nil
	}
	reviewed = normalizeCompletionAuditReviewPaths(reviewed)
	if len(reviewed) == 0 {
		return currentChanged
	}
	reviewedSet := map[string]bool{}
	for _, path := range reviewed {
		reviewedSet[path] = true
	}
	var missing []string
	for _, path := range currentChanged {
		if !reviewedSet[path] {
			missing = append(missing, path)
		}
	}
	return missing
}
