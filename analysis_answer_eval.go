package main

import (
	"fmt"
	"strings"
)

type ArchitectureAnswerEvaluation struct {
	Score      int                           `json:"score,omitempty"`
	Violations []ArchitectureAnswerViolation `json:"violations,omitempty"`
}

type ArchitectureAnswerViolation struct {
	Code     string `json:"code,omitempty"`
	Severity int    `json:"severity,omitempty"`
	Message  string `json:"message,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}

func evaluateArchitectureAnswerAgainstFacts(answer string, pack ArchitectureFactPack) ArchitectureAnswerEvaluation {
	answer = strings.TrimSpace(answer)
	if answer == "" || !architectureFactPackHasData(pack) {
		return ArchitectureAnswerEvaluation{Score: 100}
	}
	evaluation := ArchitectureAnswerEvaluation{Score: 100}
	evaluation.Violations = append(evaluation.Violations, evaluateArchitectureTopLevelDirectoryAnswer(answer, pack)...)
	evaluation.Violations = append(evaluation.Violations, evaluateArchitectureAnchorRelabeling(answer, pack)...)
	evaluation.Violations = append(evaluation.Violations, evaluateArchitectureExactAnchorLabeling(answer, pack)...)
	evaluation.Violations = append(evaluation.Violations, evaluateArchitectureFlowSeparation(answer, pack)...)
	if len(evaluation.Violations) > 0 {
		score := 100
		for _, violation := range evaluation.Violations {
			switch {
			case violation.Severity >= 3:
				score -= 30
			case violation.Severity == 2:
				score -= 15
			default:
				score -= 5
			}
		}
		if score < 0 {
			score = 0
		}
		evaluation.Score = score
	}
	return evaluation
}

func architectureAnswerHasBlockingViolations(evaluation ArchitectureAnswerEvaluation) bool {
	for _, violation := range evaluation.Violations {
		if violation.Severity >= 3 {
			return true
		}
	}
	return false
}

func evaluateArchitectureTopLevelDirectoryAnswer(answer string, pack ArchitectureFactPack) []ArchitectureAnswerViolation {
	if len(pack.TopLevelDirectories) == 0 {
		return nil
	}
	rootSet := map[string]struct{}{}
	for _, dir := range pack.TopLevelDirectories {
		normalized := normalizeArchitectureAnswerPath(dir.Path)
		if normalized == "" {
			continue
		}
		rootSet[strings.ToLower(normalized)] = struct{}{}
		rootSet[strings.ToLower(strings.TrimSuffix(normalized, "/"))] = struct{}{}
	}
	exclusionSet := map[string]struct{}{}
	for _, path := range pack.TopLevelNonDirectoryExclusions {
		normalized := normalizeArchitectureAnswerPath(path)
		if normalized == "" {
			continue
		}
		exclusionSet[strings.ToLower(normalized)] = struct{}{}
		exclusionSet[strings.ToLower(strings.TrimSuffix(normalized, "/"))] = struct{}{}
	}
	lines := strings.Split(answer, "\n")
	violations := []ArchitectureAnswerViolation{}
	for index, line := range lines {
		cell := architectureAnswerMarkdownFirstCellPath(line)
		if cell == "" || !architectureAnswerLineLooksLikeTopLevelSection(lines, index) {
			continue
		}
		normalized := normalizeArchitectureAnswerPath(cell)
		if normalized == "" || !architectureAnswerLooksLikePath(normalized) {
			continue
		}
		lower := strings.ToLower(strings.TrimSuffix(normalized, "/"))
		if _, ok := exclusionSet[lower]; ok {
			violations = append(violations, ArchitectureAnswerViolation{
				Code:     "top_level_exclusion_listed",
				Severity: 3,
				Message:  fmt.Sprintf("%s is explicitly excluded from top-level directory rows.", normalized),
				Evidence: strings.TrimSpace(line),
			})
			continue
		}
		if _, ok := rootSet[lower]; ok {
			continue
		}
		if _, ok := rootSet[strings.ToLower(normalized)]; ok {
			continue
		}
		violations = append(violations, ArchitectureAnswerViolation{
			Code:     "top_level_closed_set_violation",
			Severity: 3,
			Message:  fmt.Sprintf("%s is not in the closed top-level directory set.", normalized),
			Evidence: strings.TrimSpace(line),
		})
	}
	return violations
}

func evaluateArchitectureAnchorRelabeling(answer string, pack ArchitectureFactPack) []ArchitectureAnswerViolation {
	if len(pack.CriticalAnchors) == 0 {
		return nil
	}
	accessorLocations := map[string]ArchitectureAnchorFact{}
	for _, anchor := range pack.CriticalAnchors {
		role := strings.ToLower(anchor.Role)
		symbol := strings.ToLower(anchor.Symbol)
		if !strings.Contains(role, "accessor") && !strings.Contains(symbol, "get") {
			continue
		}
		location := strings.TrimSpace(anchor.Location)
		if location == "" {
			continue
		}
		accessorLocations[strings.ToLower(location)] = anchor
	}
	if len(accessorLocations) == 0 {
		return nil
	}
	violations := []ArchitectureAnswerViolation{}
	for _, line := range strings.Split(answer, "\n") {
		lower := strings.ToLower(line)
		if !architectureAnswerMentionsLifecycle(lower) {
			continue
		}
		for location, anchor := range accessorLocations {
			if !architectureAnswerLineContainsLocation(line, location) {
				continue
			}
			if strings.Contains(lower, strings.ToLower(anchor.Symbol)) {
				continue
			}
			violations = append(violations, ArchitectureAnswerViolation{
				Code:     "accessor_anchor_relabelled_as_lifecycle",
				Severity: 3,
				Message:  fmt.Sprintf("%s is an accessor anchor and must not be relabelled as lifecycle/teardown.", anchor.Location),
				Evidence: strings.TrimSpace(line),
			})
		}
	}
	return violations
}

func evaluateArchitectureExactAnchorLabeling(answer string, pack ArchitectureFactPack) []ArchitectureAnswerViolation {
	if len(pack.CriticalAnchors) == 0 {
		return nil
	}
	violations := []ArchitectureAnswerViolation{}
	for _, anchor := range pack.CriticalAnchors {
		location := strings.TrimSpace(anchor.Location)
		symbol := strings.TrimSpace(anchor.Symbol)
		if location == "" || symbol == "" {
			continue
		}
		for _, line := range strings.Split(answer, "\n") {
			if !architectureAnswerLineContainsLocation(line, location) {
				continue
			}
			if strings.Contains(strings.ToLower(line), strings.ToLower(symbol)) {
				continue
			}
			if !architectureAnswerLineLooksLikeAnchorRow(line) {
				continue
			}
			violations = append(violations, ArchitectureAnswerViolation{
				Code:     "exact_anchor_symbol_missing",
				Severity: 2,
				Message:  fmt.Sprintf("%s must be labelled with exact symbol %s.", location, symbol),
				Evidence: strings.TrimSpace(line),
			})
		}
	}
	return violations
}

func evaluateArchitectureFlowSeparation(answer string, pack ArchitectureFactPack) []ArchitectureAnswerViolation {
	if !analysisContainsStringCI(pack.DomainHints, "windows_driver") {
		return nil
	}
	violations := []ArchitectureAnswerViolation{}
	for _, line := range strings.Split(answer, "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		if lower == "" || !containsAny(lower, "->", "→") {
			continue
		}
		if containsAny(lower, "separate", "별개", "분리") {
			continue
		}
		if containsAny(lower, "irp_mj_device_control", "deviceiocontrol") &&
			containsAny(lower, "irp_mj_create", "control-open", "request-origin", "요청 출처", "오픈 검증") {
			violations = append(violations, ArchitectureAnswerViolation{
				Code:     "irp_create_and_device_control_collapsed",
				Severity: 3,
				Message:  "IRP_MJ_CREATE/control-open validation and IRP_MJ_DEVICE_CONTROL command dispatch must remain separate paths unless call-edge evidence connects them.",
				Evidence: strings.TrimSpace(line),
			})
		}
		if containsAny(lower, "initialize", "driverentry", "초기화") &&
			containsAny(lower, "startobjectfilter", "obregistercallbacks", "runtime callback", "런타임 콜백") &&
			!containsAny(lower, "not", "아님", "않") {
			violations = append(violations, ArchitectureAnswerViolation{
				Code:     "runtime_registration_collapsed_into_init",
				Severity: 3,
				Message:  "Runtime callback/filter registration must not be placed in initialization unless explicit call-edge evidence connects them.",
				Evidence: strings.TrimSpace(line),
			})
		}
	}
	return violations
}

func architectureAnswerMarkdownFirstCellPath(line string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "|") || strings.Contains(trimmed, "---") {
		return ""
	}
	parts := strings.Split(trimmed, "|")
	if len(parts) < 3 {
		return ""
	}
	return normalizeArchitectureAnswerPath(parts[1])
}

func architectureAnswerLineLooksLikeAnchorRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "|") {
		return true
	}
	return containsAny(strings.ToLower(trimmed), "anchor", "source", "symbol", "파일", "라인", "앵커", "심볼", ":")
}

func architectureAnswerLineLooksLikeTopLevelSection(lines []string, index int) bool {
	start := index - 6
	if start < 0 {
		start = 0
	}
	for i := index; i >= start; i-- {
		lower := strings.ToLower(strings.TrimSpace(lines[i]))
		if lower == "" {
			continue
		}
		if containsAny(lower, "top-level", "top level", "root directory", "root directories", "최상위", "루트", "디렉터리", "디렉토리", "폴더") {
			return true
		}
	}
	return false
}

func architectureAnswerLooksLikePath(path string) bool {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return false
	}
	return strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") || strings.Contains(trimmed, ".")
}

func architectureAnswerMentionsLifecycle(lowerLine string) bool {
	return containsAny(lowerLine, "finalize", "unload", "teardown", "cleanup", "해제", "정리", "언로드", "라이프사이클")
}

func architectureAnswerLineContainsLocation(line string, location string) bool {
	normalizedLine := strings.ToLower(normalizeArchitectureAnswerPath(line))
	normalizedLocation := strings.ToLower(normalizeArchitectureAnswerPath(location))
	if normalizedLine == "" || normalizedLocation == "" {
		return false
	}
	return strings.Contains(normalizedLine, normalizedLocation)
}

func normalizeArchitectureAnswerPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "`*_ ")
	if idx := strings.Index(path, "]("); idx > 0 && strings.HasPrefix(path, "[") {
		path = strings.TrimPrefix(path[:idx], "[")
	}
	path = strings.Trim(path, "`*_ ")
	path = strings.ReplaceAll(path, "\\", "/")
	path = strings.TrimPrefix(path, "./")
	path = strings.Trim(path, " ")
	if strings.HasPrefix(path, "/") {
		path = strings.TrimLeft(path, "/")
	}
	return path
}
