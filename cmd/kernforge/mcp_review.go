package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
)

func (s *kernforgeMCPServer) toolReview(ctx context.Context, args map[string]any) (string, error) {
	request := strings.TrimSpace(stringValue(args, "request"))
	autoReview := strings.ToLower(strings.TrimSpace(stringValue(args, "auto_review")))
	if autoReview == "off" {
		decision := classifyAcceptanceContractRequestClassDecision(request, classifyTurnIntent(request), prefersReadOnlyAnalysisIntent(request) || looksLikeReviewInspectionOnlyRequest(request), looksLikeExplicitEditIntent(request))
		requestClass, requestClassReason := decision.RequestClass, decision.Reason
		payload := map[string]any{
			"summary":        "MCP review skipped because auto_review=off.",
			"machine_status": reviewMachineStatusWarning,
			"status_code":    0,
			"retryable":      false,
			"request_class":  requestClass,
			"lifecycle": ReviewRequestLifecycle{
				RequestClass:             requestClass,
				Phase:                    "skipped",
				Reason:                   requestClassReason,
				ClassificationConfidence: decision.Confidence,
				ClassificationAmbiguous:  decision.Ambiguous,
				AmbiguityWarnings:        decision.AmbiguityWarnings,
				Contract:                 reviewLifecycleContractForClass(requestClass),
			},
		}
		data, _ := json.MarshalIndent(payload, "", "  ")
		return "KernForge review\n\n```json\n" + string(data) + "\n```", nil
	}
	opts := ReviewHarnessOptions{
		Trigger:             "explicit_mcp",
		Target:              stringValue(args, "target"),
		Mode:                stringValue(args, "mode"),
		Request:             request,
		Paths:               stringSliceValue(args, "paths"),
		ProvidedDiff:        stringValue(args, "diff"),
		ProvidedCode:        stringValue(args, "code"),
		IncludeGitDiff:      boolValue(args, "include_git_diff", strings.TrimSpace(stringValue(args, "diff")) == "" && strings.TrimSpace(stringValue(args, "code")) == ""),
		IncludeFileContents: boolValue(args, "include_file_contents", false),
		NoModel:             boolValue(args, "no_model", false),
		AutoFollowUp:        stringValue(args, "auto_follow_up"),
		MaxContextChars:     mcpReviewMaxContextChars(args, 60000),
	}
	if strings.TrimSpace(opts.Request) == "" {
		opts.Request = "Review the requested target with the common KernForge review harness."
	}
	run, err := runReviewHarness(ctx, s.rt, opts)
	if err != nil {
		return "", err
	}
	root := ""
	if s != nil && s.rt != nil {
		root = workspaceSnapshotRoot(s.rt.workspace)
	}
	return renderReviewMCPResponseWithLatestFreshness(run, reviewLatestFreshnessForRoot(root, run), mcpMaxChars(args, 60000)), nil
}

func mcpReviewMaxContextChars(args map[string]any, fallback int) int {
	value := intValue(args, "max_context_chars", fallback)
	if value <= 0 {
		value = fallback
	}
	if value < 4000 {
		return 4000
	}
	if value > 200000 {
		return 200000
	}
	return value
}

func mcpReviewCleanPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, path := range paths {
		path = strings.TrimSpace(filepath.ToSlash(path))
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func parseMCPReviewGitStatusPaths(status string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(status, "\r\n", "\n"), "\n") {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "## ") {
			continue
		}
		if len(line) < 3 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if idx := strings.LastIndex(path, " -> "); idx >= 0 {
			path = strings.TrimSpace(path[idx+4:])
		}
		if path != "" {
			out = append(out, filepath.ToSlash(path))
		}
	}
	return analysisUniqueStrings(out)
}

func parseMCPReviewUntrackedPaths(status string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(status, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "?? ") {
			continue
		}
		path := strings.TrimSpace(strings.TrimPrefix(trimmed, "?? "))
		if path != "" && !shouldSkipMCPReviewFile(path) {
			out = append(out, filepath.ToSlash(path))
		}
	}
	return analysisUniqueStrings(out)
}

func filterMCPReviewPaths(paths []string, allowed []string) []string {
	if len(allowed) == 0 {
		return paths
	}
	allowed = mcpReviewCleanPaths(allowed)
	var out []string
	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimSpace(path))
		for _, prefix := range allowed {
			if path == prefix || strings.HasPrefix(path, strings.TrimSuffix(prefix, "/")+"/") {
				out = append(out, path)
				break
			}
		}
	}
	return analysisUniqueStrings(out)
}

func shouldSkipMCPReviewFile(path string) bool {
	normalized := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	if normalized == "" {
		return true
	}
	if strings.HasPrefix(normalized, ".git/") ||
		strings.HasPrefix(normalized, ".kernforge/") ||
		strings.HasPrefix(normalized, "sessions/") ||
		strings.HasPrefix(normalized, "release/") {
		return true
	}
	switch normalized {
	case "verification-history.json", "verify-history.json":
		return true
	}
	switch filepath.Ext(normalized) {
	case ".exe", ".dll", ".sys", ".pdb", ".obj", ".lib", ".bin", ".zip", ".7z", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".pdf":
		return true
	default:
		return false
	}
}
