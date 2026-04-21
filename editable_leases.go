package main

import (
	"path"
	"regexp"
	"strings"
)

var editableLeasePathHintPattern = regexp.MustCompile(`(?i)(?:[a-z0-9_.-]+[\\/])+[a-z0-9_.-]+|[a-z0-9_.-]+\.[a-z0-9_.-]+`)

func effectiveEditableLeasePaths(node TaskNode, profile SpecialistSubagentProfile) []string {
	if lease := normalizeTaskStateList(node.EditableLeasePaths, 32); len(lease) > 0 {
		return lease
	}
	return specialistOwnershipPaths(profile, node.EditableOwnershipPaths)
}

func deriveEditableLeasePaths(graph *TaskGraph, node TaskNode, profile SpecialistSubagentProfile) ([]string, string) {
	basePatterns := specialistOwnershipPaths(profile, node.EditableOwnershipPaths)
	if len(basePatterns) == 0 {
		return nil, ""
	}
	if existing := normalizeTaskStateList(node.EditableLeasePaths, 32); len(existing) > 0 && editableLeaseFitsOwnership(existing, basePatterns) {
		return existing, firstNonBlankString(strings.TrimSpace(node.EditableLeaseReason), "existing-lease")
	}
	if explicit := selectEditableLeasePathHints(node, basePatterns); len(explicit) > 0 {
		filtered := filterClaimedEditableLeasePaths(graph, node, profile, explicit)
		if len(filtered) > 0 {
			return filtered, "path-hints"
		}
		return explicit, "path-hints"
	}
	if hinted := selectEditableLeasePatternHints(node, basePatterns); len(hinted) > 0 {
		filtered := filterClaimedEditableLeasePaths(graph, node, profile, hinted)
		if len(filtered) > 0 {
			if len(filtered) != len(hinted) {
				return filtered, "pattern-hints-disjoint"
			}
			return filtered, "pattern-hints"
		}
		return hinted, "pattern-hints"
	}
	return basePatterns, "ownership-fallback"
}

func editableLeaseFitsOwnership(leasePaths []string, ownership []string) bool {
	if len(leasePaths) == 0 || len(ownership) == 0 {
		return false
	}
	normalizedOwnership := normalizeTaskStateList(ownership, 32)
	for _, lease := range normalizeTaskStateList(leasePaths, 32) {
		fits := false
		for _, base := range normalizedOwnership {
			if normalizeOwnershipPattern(lease) == normalizeOwnershipPattern(base) {
				fits = true
				break
			}
			if !strings.ContainsAny(lease, "*?") && ownershipPatternMatches(lease, base) {
				fits = true
				break
			}
		}
		if !fits {
			return false
		}
	}
	return true
}

func taskNodeEditableLeaseText(node TaskNode) string {
	return strings.ToLower(strings.Join([]string{
		node.Title,
		node.MicroWorkerBrief,
		node.ReadOnlyWorkerSummary,
		node.LifecycleNote,
		node.LastFailure,
		node.EditableReason,
	}, " "))
}

func selectEditableLeasePathHints(node TaskNode, basePatterns []string) []string {
	text := taskNodeEditableLeaseText(node)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	matches := editableLeasePathHintPattern.FindAllString(text, -1)
	hints := make([]string, 0, len(matches))
	for _, raw := range matches {
		hint := normalizeEditableLeasePathHint(raw)
		if hint == "" {
			continue
		}
		if len(basePatterns) > 0 && !editableLeasePathWithinOwnership(hint, basePatterns) {
			continue
		}
		hints = append(hints, hint)
	}
	return normalizeTaskStateList(hints, 32)
}

func bestEffortParallelEditableLeasePaths(node TaskNode) []string {
	if !strings.EqualFold(strings.TrimSpace(node.Kind), "edit") {
		return nil
	}
	if lease := normalizeTaskStateList(node.EditableLeasePaths, 32); len(lease) > 0 {
		return lease
	}
	basePatterns := normalizeTaskStateList(node.EditableOwnershipPaths, 32)
	if explicit := selectEditableLeasePathHints(node, basePatterns); len(explicit) > 0 {
		return explicit
	}
	if hinted := selectEditableLeasePatternHints(node, basePatterns); len(hinted) > 0 {
		if filtered := filterParallelSafeEditableLeasePatterns(hinted); len(filtered) > 0 {
			return filtered
		}
	}
	return filterParallelSafeEditableLeasePatterns(basePatterns)
}

func filterParallelSafeEditableLeasePatterns(patterns []string) []string {
	filtered := make([]string, 0, len(patterns))
	for _, pattern := range normalizeTaskStateList(patterns, 32) {
		if editableLeasePatternSafeForParallel(pattern) {
			filtered = append(filtered, pattern)
		}
	}
	return normalizeTaskStateList(filtered, 32)
}

func editableLeasePatternSafeForParallel(pattern string) bool {
	normalized := normalizeOwnershipPattern(pattern)
	if normalized == "" || normalized == "**" {
		return false
	}
	if !strings.ContainsAny(normalized, "*?") {
		return true
	}
	if !strings.Contains(normalized, "/") {
		return false
	}
	return editableLeasePatternBaseDir(normalized) != ""
}

func editableLeasePatternBaseDir(pattern string) string {
	normalized := normalizeOwnershipPattern(pattern)
	if normalized == "" || normalized == "." {
		return ""
	}
	if !strings.ContainsAny(normalized, "*?") {
		dir := path.Dir(normalized)
		if dir == "." {
			return ""
		}
		return dir
	}
	index := strings.IndexAny(normalized, "*?")
	if index < 0 {
		return ""
	}
	prefix := strings.TrimSuffix(normalized[:index], "/")
	if prefix == "" {
		return ""
	}
	if index > 0 && normalized[index-1] != '/' {
		prefix = path.Dir(prefix)
	}
	if prefix == "." {
		return ""
	}
	return prefix
}

func firstConcreteEditableLeasePath(patterns []string) string {
	for _, pattern := range normalizeTaskStateList(patterns, 32) {
		if strings.ContainsAny(pattern, "*?") {
			continue
		}
		if strings.TrimSpace(pattern) == "" {
			continue
		}
		return pattern
	}
	return ""
}

func firstEditableLeaseBaseDir(patterns []string) string {
	for _, pattern := range normalizeTaskStateList(patterns, 32) {
		if baseDir := editableLeasePatternBaseDir(pattern); baseDir != "" {
			return baseDir
		}
	}
	return ""
}

func editableLeaseCollectionsOverlap(left []string, right []string) bool {
	for _, leftPattern := range normalizeTaskStateList(left, 32) {
		for _, rightPattern := range normalizeTaskStateList(right, 32) {
			if editableLeasePatternsOverlap(leftPattern, rightPattern) {
				return true
			}
		}
	}
	return false
}

func normalizeEditableLeasePathHint(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.Contains(trimmed, "://") {
		return ""
	}
	trimmed = strings.Trim(trimmed, "\"'`()[]{}<>,:;")
	trimmed = strings.ReplaceAll(trimmed, "\\", "/")
	trimmed = strings.TrimPrefix(trimmed, "./")
	trimmed = strings.TrimPrefix(trimmed, "/")
	trimmed = path.Clean(trimmed)
	if trimmed == "." || trimmed == "" || strings.HasPrefix(trimmed, "../") {
		return ""
	}
	return strings.ToLower(trimmed)
}

func editableLeasePathWithinOwnership(relPath string, basePatterns []string) bool {
	for _, pattern := range normalizeTaskStateList(basePatterns, 32) {
		if ownershipPatternMatches(relPath, pattern) {
			return true
		}
	}
	return false
}

func selectEditableLeasePatternHints(node TaskNode, basePatterns []string) []string {
	text := taskNodeEditableLeaseText(node)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	selected := make([]string, 0, len(basePatterns))
	for _, pattern := range normalizeTaskStateList(basePatterns, 32) {
		if ownershipPatternHintMatchesText(pattern, text) {
			selected = append(selected, pattern)
		}
	}
	return normalizeTaskStateList(selected, 32)
}

func ownershipPatternHintMatchesText(pattern string, text string) bool {
	normalized := normalizeOwnershipPattern(pattern)
	if normalized == "" || normalized == "**" {
		return false
	}
	for _, token := range ownershipPatternHintTokens(normalized) {
		if editableLeaseTextContainsToken(text, token) {
			return true
		}
	}
	return false
}

func ownershipPatternHintTokens(pattern string) []string {
	if strings.TrimSpace(pattern) == "" {
		return nil
	}
	tokens := make([]string, 0, 6)
	for _, field := range strings.FieldsFunc(pattern, func(r rune) bool {
		return r == '/' || r == '*' || r == '?' || r == '.' || r == '-' || r == '_'
	}) {
		token := strings.TrimSpace(strings.ToLower(field))
		if len(token) < 3 {
			continue
		}
		tokens = append(tokens, token)
		if strings.HasSuffix(token, "s") && len(token) > 4 {
			tokens = append(tokens, strings.TrimSuffix(token, "s"))
		}
	}
	return normalizeTaskStateList(tokens, 16)
}

func editableLeaseTextContainsToken(text string, token string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	token = strings.ToLower(strings.TrimSpace(token))
	if text == "" || token == "" {
		return false
	}
	if strings.Contains(text, token) {
		return true
	}
	if strings.HasSuffix(token, "s") && len(token) > 4 && strings.Contains(text, strings.TrimSuffix(token, "s")) {
		return true
	}
	return false
}

func filterClaimedEditableLeasePaths(graph *TaskGraph, node TaskNode, profile SpecialistSubagentProfile, leasePaths []string) []string {
	if graph == nil || len(leasePaths) == 0 {
		return normalizeTaskStateList(leasePaths, 32)
	}
	filtered := make([]string, 0, len(leasePaths))
	for _, lease := range normalizeTaskStateList(leasePaths, 32) {
		claimed := false
		for _, other := range graph.Nodes {
			if strings.EqualFold(strings.TrimSpace(other.ID), strings.TrimSpace(node.ID)) {
				continue
			}
			status := canonicalTaskNodeStatus(other.Status)
			if status == "completed" || status == "failed" || status == "stale" || status == "superseded" || status == "canceled" || status == "preempted" {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(other.EditableSpecialist), strings.TrimSpace(profile.Name)) {
				continue
			}
			for _, otherLease := range normalizeTaskStateList(other.EditableLeasePaths, 32) {
				if editableLeasePatternsOverlap(lease, otherLease) {
					claimed = true
					break
				}
			}
			if claimed {
				break
			}
		}
		if !claimed {
			filtered = append(filtered, lease)
		}
	}
	if len(filtered) == 0 {
		return normalizeTaskStateList(leasePaths, 32)
	}
	return filtered
}

func editableLeasePatternsOverlap(left string, right string) bool {
	left = normalizeOwnershipPattern(left)
	right = normalizeOwnershipPattern(right)
	if left == "" || right == "" {
		return false
	}
	if left == right || left == "**" || right == "**" {
		return true
	}
	if !strings.ContainsAny(left, "*?") && ownershipPatternMatches(left, right) {
		return true
	}
	if !strings.ContainsAny(right, "*?") && ownershipPatternMatches(right, left) {
		return true
	}
	return false
}
