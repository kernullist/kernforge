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
		decision = applyReviewLifecycleKindToDecision(decision, request, classifyTurnIntent(request), "", "mcp_auto_review_off")
		requestClass, requestClassReason := decision.RequestClass, decision.Reason
		lifecycle := ReviewRequestLifecycle{
			RequestClass:             requestClass,
			LifecycleKind:            decision.LifecycleKind,
			MixedFlow:                decision.MixedFlow,
			SecondaryRequestClasses:  decision.SecondaryRequestClasses,
			Phase:                    "skipped",
			Reason:                   requestClassReason,
			ClassificationConfidence: decision.Confidence,
			ClassificationAmbiguous:  decision.Ambiguous,
			AmbiguityWarnings:        decision.AmbiguityWarnings,
			Contract:                 reviewLifecycleContractForClass(requestClass),
			Timeline: []ReviewLifecyclePhase{{
				Phase:          reviewLifecyclePhaseClassifiedRequest,
				Status:         reviewTimelineStatusPassed,
				Reason:         requestClassReason,
				EvidenceRef:    "mcp.auto_review",
				NextSafeAction: "set auto_review=on or omit auto_review=off to run the review harness",
				NextCommand:    "kernforge_review",
			}, {
				Phase:          "skipped",
				Status:         reviewTimelineStatusSkipped,
				Reason:         "MCP review skipped because auto_review=off",
				EvidenceRef:    "mcp.arguments.auto_review",
				NextSafeAction: "run kernforge_review with auto_review=on when review evidence is required",
				NextCommand:    "kernforge_review",
			}},
		}
		lifecycle.Normalize()
		compact := &ReviewCompactStatus{
			RequestClass:              requestClass,
			LifecycleKind:             decision.LifecycleKind,
			MixedFlow:                 decision.MixedFlow,
			SecondaryRequestClasses:   decision.SecondaryRequestClasses,
			ClassificationConfidence:  decision.Confidence,
			ClassificationAmbiguous:   decision.Ambiguous,
			ClassificationAmbiguity:   decision.AmbiguityWarnings,
			CurrentLifecyclePhase:     "skipped",
			RouteMode:                 reviewRouteModeDeterministicOnly,
			ReviewGateStatus:          "skipped",
			RepairGateStatus:          "skipped",
			DocumentGateStatus:        reviewRuntimeGateDocumentStatus(nil, requestClass),
			VerificationGateStatus:    "skipped",
			FinalAnswerContractStatus: reviewFinalAnswerContractStatusPending,
			NextRecommendedCommand:    "kernforge_review",
		}
		compact.Normalize()
		payload := map[string]any{
			"summary":                      "MCP review skipped because auto_review=off.",
			"machine_status":               reviewMachineStatusWarning,
			"status_code":                  0,
			"retryable":                    false,
			"request_class":                requestClass,
			"lifecycle_kind":               decision.LifecycleKind,
			"mixed_flow":                   decision.MixedFlow,
			"secondary_request_classes":    decision.SecondaryRequestClasses,
			"lifecycle":                    lifecycle,
			"lifecycle_timeline":           lifecycle.Timeline,
			"compact_status":               compact,
			"blocker_summary":              (*ReviewBlockerSummary)(nil),
			"route_quality":                ReviewRouteQualitySummary{Status: "skipped"},
			"final_answer_contract_status": reviewFinalAnswerContractStatusForClass(requestClass, nil, nil, ""),
			"next_recommended_command": map[string]any{
				"command": "kernforge_review",
				"reason":  "auto_review=off skipped the review harness",
				"safety":  "read_only",
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
