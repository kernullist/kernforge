package main

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

func normalizeOwnershipPattern(pattern string) string {
	normalized := strings.TrimSpace(pattern)
	if normalized == "" {
		return ""
	}
	normalized = strings.ReplaceAll(normalized, "\\", "/")
	normalized = strings.TrimPrefix(normalized, "./")
	normalized = strings.TrimPrefix(normalized, "/")
	normalized = strings.TrimSpace(normalized)
	if normalized == "" {
		return ""
	}
	return strings.ToLower(normalized)
}

func ownershipRelativePath(root string, absolutePath string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("ownership root is not configured")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	targetAbs, err := filepath.Abs(absolutePath)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return ".", nil
	}
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: path is outside the editable ownership root: %s", ErrEditTargetMismatch, absolutePath)
	}
	return strings.ToLower(filepath.ToSlash(filepath.Clean(rel))), nil
}

func ownershipPatternRegex(pattern string) string {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch ch {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				b.WriteString(".*")
				i++
				continue
			}
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	b.WriteString("$")
	return b.String()
}

func ownershipPatternMatches(relPath string, pattern string) bool {
	normalizedPattern := normalizeOwnershipPattern(pattern)
	if normalizedPattern == "" {
		return false
	}
	if normalizedPattern == "**" {
		return true
	}
	target := strings.ToLower(strings.TrimSpace(relPath))
	if target == "" {
		target = "."
	}
	if !strings.Contains(normalizedPattern, "/") {
		target = strings.ToLower(path.Base(target))
	}
	re, err := regexp.Compile(ownershipPatternRegex(normalizedPattern))
	if err != nil {
		return false
	}
	return re.MatchString(target)
}

func editableOwnershipMatch(root string, absolutePath string, patterns []string) (bool, string, string, error) {
	if len(patterns) == 0 {
		return true, "", "", nil
	}
	relPath, err := ownershipRelativePath(root, absolutePath)
	if err != nil {
		return false, "", "", err
	}
	normalized := make([]string, 0, len(patterns))
	for _, raw := range patterns {
		pattern := normalizeOwnershipPattern(raw)
		if pattern == "" {
			continue
		}
		normalized = append(normalized, pattern)
		if ownershipPatternMatches(relPath, pattern) {
			return true, relPath, pattern, nil
		}
	}
	return false, relPath, "", nil
}

func enforceEditableOwnership(root string, absolutePath string, specialist string, patterns []string) error {
	allowed, relPath, matchedPattern, err := editableOwnershipMatch(root, absolutePath, patterns)
	if err != nil {
		return err
	}
	if allowed {
		_ = matchedPattern
		return nil
	}
	return fmt.Errorf(
		"%w: path %s is outside editable ownership for specialist %s (allowed: %s)",
		ErrEditTargetMismatch,
		firstNonBlankString(relPath, absolutePath),
		firstNonBlankString(strings.TrimSpace(specialist), "unknown"),
		strings.Join(normalizeTaskStateList(patterns, 32), ", "),
	)
}
