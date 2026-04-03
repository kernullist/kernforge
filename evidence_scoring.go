package main

import (
	"path/filepath"
	"strings"
	"time"
)

type EvidenceScoringContext struct {
	Now                 time.Time
	RecentWorkspace     []EvidenceRecord
	RecentCategoryFails map[string]int
	RecentSubjectFails  map[string]int
	RecentSignalFails   map[string]int
}

func buildEvidenceScoringContext(records []EvidenceRecord, workspace string, now time.Time) EvidenceScoringContext {
	ctx := EvidenceScoringContext{
		Now:                 now,
		RecentCategoryFails: map[string]int{},
		RecentSubjectFails:  map[string]int{},
		RecentSignalFails:   map[string]int{},
	}
	workspace = normalizePersistentMemoryWorkspace(workspace)
	for _, record := range records {
		if workspace != "" && workspaceAffinityScore(workspace, record.Workspace) == 0 {
			continue
		}
		ctx.RecentWorkspace = append(ctx.RecentWorkspace, record)
		if !strings.EqualFold(strings.TrimSpace(record.Outcome), "failed") {
			continue
		}
		if !record.CreatedAt.IsZero() && now.Sub(record.CreatedAt) > 72*time.Hour {
			continue
		}
		if category := strings.ToLower(strings.TrimSpace(record.Category)); category != "" {
			ctx.RecentCategoryFails[category]++
		}
		if subject := strings.ToLower(strings.TrimSpace(record.Subject)); subject != "" {
			ctx.RecentSubjectFails[subject]++
		}
		if signal := strings.ToLower(strings.TrimSpace(record.SignalClass)); signal != "" {
			ctx.RecentSignalFails[signal]++
		}
	}
	return ctx
}

func scoreEvidenceRecord(record EvidenceRecord, ctx EvidenceScoringContext) EvidenceRecord {
	record.SignalClass = deriveEvidenceSignalClass(record)
	record.Severity, record.Confidence, record.SeverityReasons = deriveEvidenceSeverity(record, ctx)
	record.RiskScore = deriveEvidenceRiskScore(record, ctx)
	record.SeverityReasons = uniqueStrings(record.SeverityReasons)
	return record
}

func deriveEvidenceSignalClass(record EvidenceRecord) string {
	if existing := strings.ToLower(strings.TrimSpace(record.SignalClass)); existing != "" {
		return existing
	}
	for _, tag := range record.Tags {
		switch strings.ToLower(strings.TrimSpace(tag)) {
		case "signing":
			return "signing"
		case "symbol", "symbols":
			return "symbols"
		case "package":
			return "package"
		case "verifier":
			return "verifier"
		case "provider":
			return "provider"
		case "xml":
			return "xml"
		case "schema":
			return "schema"
		case "integrity":
			return "integrity"
		case "evasion":
			return "evasion"
		case "false_positive":
			return "false_positive"
		case "performance", "perf":
			return "performance"
		case "runtime":
			return "runtime"
		}
	}
	lowerSubject := strings.ToLower(filepath.ToSlash(strings.TrimSpace(record.Subject)))
	switch {
	case strings.HasSuffix(lowerSubject, ".sys"), strings.HasSuffix(lowerSubject, ".cat"):
		return "signing"
	case strings.HasSuffix(lowerSubject, ".inf"):
		return "package"
	case strings.HasSuffix(lowerSubject, ".man"), strings.HasSuffix(lowerSubject, ".mc"):
		return "provider"
	case strings.HasSuffix(lowerSubject, ".xml"):
		return "xml"
	}
	switch strings.ToLower(strings.TrimSpace(record.Category)) {
	case "driver":
		return "runtime"
	case "telemetry":
		return "provider"
	case "memory-scan":
		return "evasion"
	case "unreal":
		return "integrity"
	default:
		return "runtime"
	}
}

func deriveEvidenceSeverity(record EvidenceRecord, ctx EvidenceScoringContext) (string, string, []string) {
	if existing := strings.ToLower(strings.TrimSpace(record.Severity)); existing != "" {
		confidence := strings.ToLower(strings.TrimSpace(record.Confidence))
		if confidence == "" {
			confidence = "medium"
		}
		reasons := append([]string(nil), record.SeverityReasons...)
		if len(reasons) == 0 {
			reasons = append(reasons, "pre-scored evidence severity")
		}
		return existing, confidence, reasons
	}
	category := strings.ToLower(strings.TrimSpace(record.Category))
	outcome := strings.ToLower(strings.TrimSpace(record.Outcome))
	signal := strings.ToLower(strings.TrimSpace(record.SignalClass))
	reasons := []string{}
	if outcome == "failed" {
		reasons = append(reasons, "failed evidence outcome")
	}
	if category != "" {
		reasons = append(reasons, category+" evidence")
	}
	if signal != "" {
		reasons = append(reasons, signal+" signal detected")
	}
	repeatCount := evidenceRepeatCount(record, ctx)
	if repeatCount >= 2 {
		reasons = append(reasons, "recent repeated failure pattern")
	}

	confidence := "medium"
	switch record.Kind {
	case "verification_artifact", "verification_failure":
		confidence = "high"
	case "verification_category":
		confidence = "medium"
	}
	if outcome != "failed" {
		confidence = "low"
	}

	severity := "low"
	switch category {
	case "driver":
		switch {
		case outcome == "failed" && signal == "signing":
			severity = "critical"
		case outcome == "failed" && (signal == "symbols" || signal == "package" || signal == "verifier"):
			severity = "high"
		case outcome == "failed":
			severity = "high"
		case outcome == "passed":
			severity = "low"
		}
	case "telemetry":
		switch {
		case outcome == "failed" && signal == "provider":
			severity = "high"
		case outcome == "failed" && signal == "xml":
			severity = "medium"
		case outcome == "failed" && signal == "schema":
			severity = "medium"
		case outcome == "failed":
			severity = "medium"
		}
	case "memory-scan":
		switch {
		case outcome == "failed" && signal == "evasion":
			severity = "high"
		case outcome == "failed" && signal == "false_positive":
			severity = "medium"
		case outcome == "failed" && signal == "performance":
			severity = "low"
		case outcome == "failed":
			severity = "medium"
		}
	case "unreal":
		switch {
		case outcome == "failed" && signal == "integrity":
			severity = "high"
		case outcome == "failed" && signal == "schema":
			severity = "medium"
		case outcome == "failed":
			severity = "medium"
		}
	default:
		if outcome == "failed" {
			severity = "medium"
		}
	}
	if outcome == "failed" && repeatCount >= 2 {
		switch severity {
		case "high":
			severity = "critical"
		case "medium":
			severity = "high"
		}
	}
	return severity, confidence, reasons
}

func deriveEvidenceRiskScore(record EvidenceRecord, ctx EvidenceScoringContext) int {
	if record.RiskScore > 0 {
		if record.RiskScore > 100 {
			return 100
		}
		return record.RiskScore
	}
	score := 10
	switch strings.ToLower(strings.TrimSpace(record.Category)) {
	case "driver":
		score = 35
	case "memory-scan":
		score = 28
	case "telemetry":
		score = 22
	case "unreal":
		score = 18
	}
	switch strings.ToLower(strings.TrimSpace(record.Outcome)) {
	case "failed":
		score += 30
	case "":
		score += 5
	}
	switch strings.ToLower(strings.TrimSpace(record.SignalClass)) {
	case "signing":
		score += 25
	case "symbols":
		score += 20
	case "package":
		score += 20
	case "verifier":
		score += 18
	case "provider":
		score += 16
	case "xml":
		score += 12
	case "integrity":
		score += 14
	case "schema":
		score += 10
	case "evasion":
		score += 18
	case "false_positive":
		score += 12
	case "performance":
		score += 5
	case "runtime":
		score += 15
	}
	repeatCount := evidenceRepeatCount(record, ctx)
	switch {
	case repeatCount >= 3:
		score += 15
	case repeatCount >= 2:
		score += 8
	}
	if !record.CreatedAt.IsZero() {
		age := ctx.Now.Sub(record.CreatedAt)
		switch {
		case age <= 24*time.Hour:
			score += 10
		case age <= 72*time.Hour:
			score += 5
		}
	}
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func evidenceRepeatCount(record EvidenceRecord, ctx EvidenceScoringContext) int {
	if !strings.EqualFold(strings.TrimSpace(record.Outcome), "failed") {
		return 0
	}
	subjectKey := strings.ToLower(strings.TrimSpace(record.Subject))
	if subjectKey != "" && ctx.RecentSubjectFails[subjectKey] > 0 {
		return ctx.RecentSubjectFails[subjectKey] + 1
	}
	signalKey := strings.ToLower(strings.TrimSpace(record.SignalClass))
	if signalKey != "" && ctx.RecentSignalFails[signalKey] > 0 {
		return ctx.RecentSignalFails[signalKey] + 1
	}
	categoryKey := strings.ToLower(strings.TrimSpace(record.Category))
	if categoryKey != "" && ctx.RecentCategoryFails[categoryKey] > 0 {
		return ctx.RecentCategoryFails[categoryKey] + 1
	}
	return 1
}
